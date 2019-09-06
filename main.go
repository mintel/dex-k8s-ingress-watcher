//
// An application which watches for Ingresses and configures Dex clients via
// gRPC dynamically, based on Ingress annotations.
//
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"github.com/etherlabsio/healthcheck"
	"io/ioutil"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coreos/dex/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	log "github.com/sirupsen/logrus"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

// App struct, one per time to use as resource handlers
type IngressClient struct {
	dexClient api.DexClient
}

type ConfigMapClient struct {
	dexClient api.DexClient
}

type SecretClient struct {
	dexClient api.DexClient
}

const (
	// Define annotations we check for in the watched resources
	AnnotationDexStaticClientId          = "mintel.com/dex-k8s-ingress-watcher-client-id"
	AnnotationDexStaticClientName        = "mintel.com/dex-k8s-ingress-watcher-client-name"
	AnnotationDexStaticClientRedirectURI = "mintel.com/dex-k8s-ingress-watcher-redirect-uri"
	AnnotationDexStaticClientSecret      = "mintel.com/dex-k8s-ingress-watcher-secret"
	SyncPeriodInMinutes                  = 10
)

// Label Selector for Configmap and Secret to watch
// Can't define a CONSTANT map
var configMapSecretsSelectorLabels = labels.SelectorFromSet(labels.Set(map[string]string{"mintel.com/dex-k8s-ingress-watcher": "enabled"})).String()

// Return a new Dex Client to perform gRPC calls with
func newDexClient(grpcAddress string, caPath string, clientCrtPath string, clientKeyPath string) api.DexClient {
	if caPath != "" && clientCrtPath != "" && clientKeyPath != "" {
		cPool := x509.NewCertPool()

		caCert, err := ioutil.ReadFile(caPath)
		exitOnError(err)

		if cPool.AppendCertsFromPEM(caCert) != true {
			log.Errorf("failed to parse CA crt")
		}

		clientCert, err := tls.LoadX509KeyPair(clientCrtPath, clientKeyPath)
		exitOnError(err)

		clientTLSConfig := &tls.Config{
			RootCAs:      cPool,
			Certificates: []tls.Certificate{clientCert},
		}
		creds := credentials.NewTLS(clientTLSConfig)
		conn, err := grpc.Dial(grpcAddress, grpc.WithTransportCredentials(creds))
		exitOnError(err)
		return api.NewDexClient(conn)
	} else {
		conn, err := grpc.Dial(grpcAddress, grpc.WithInsecure())
		exitOnError(err)
		return api.NewDexClient(conn)
	}

}

// Add Dex StaticClient via gRPC
func addDexStaticClient(
	c api.DexClient,
	kind string,
	name string,
	namespace string,
	static_client_id string,
	static_client_name string,
	static_client_redirect_uri string,
	static_client_secret string) {

	log.Infof("Registering %s '%s' with static client '%s' at callback '%s'",
		kind,
		name,
		static_client_id,
		static_client_redirect_uri)

	redirect_uris := strings.Split(static_client_redirect_uri, ",")
	for i := range redirect_uris {
		redirect_uris[i] = strings.TrimSpace(redirect_uris[i])
	}

	req := &api.CreateClientReq{
		Client: &api.Client{
			Id:           static_client_id,
			Name:         static_client_name,
			Secret:       static_client_secret,
			RedirectUris: redirect_uris,
		},
	}

	resp, err := c.CreateClient(context.TODO(), req)
	exitOnError(err)
	if resp.AlreadyExists {
		log.Warnf("Dex gPRC: client already exists for %s '%s' from namespace '%s'", kind, name, namespace)
	} else {
		log.Infof("Dex gRPC: Successfully created client for %s '%s' from namespace '%s'", kind, name, namespace)
	}
}

// Delete Dex StaticClient via gRPC
func deleteDexStaticClient(
	c api.DexClient,
	kind string,
	name string,
	namespace string,
	static_client_id string) {

	log.Infof("Deleting %s '%s' with static client '%s'", kind, name, static_client_id)

	req := &api.DeleteClientReq{
		Id: static_client_id,
	}

	resp, err := c.DeleteClient(context.TODO(), req)
	exitOnError(err)
	if resp.NotFound {
		log.Errorf("Dex gPRC: client '%s' could not be deleted for %s '%s' from namespace '%s' - not found '%s'", static_client_id, kind, name, namespace)
	} else {
		log.Infof("Dex gRPC: Successfully deleted client for %s '%s' from namespace '%s'", kind, name, namespace)
	}
}

// Return a new app. One per Type to be used as resource handler
func NewIngressClient(dexClient api.DexClient) *IngressClient {
	return &IngressClient{
		dexClient: dexClient,
	}
}

func NewConfigMapClient(dexClient api.DexClient) *ConfigMapClient {
	return &ConfigMapClient{
		dexClient: dexClient,
	}
}

func NewSecretClient(dexClient api.DexClient) *SecretClient {
	return &SecretClient{
		dexClient: dexClient,
	}
}

func extractAnnotations(ann map[string]string) (client_id string, client_name string, client_redirect_uri string, client_secret string, err error) {

	static_client_id, ok := ann[AnnotationDexStaticClientId]
	if !ok {
		return "", "", "", "", fmt.Errorf("missing annotation '%s'", AnnotationDexStaticClientId)
	}

	static_client_name, ok := ann[AnnotationDexStaticClientName]
	if !ok {
		// Default to using the ID
		static_client_name = static_client_id
	}

	static_client_redirect_uri, ok := ann[AnnotationDexStaticClientRedirectURI]
	if !ok {
		return "", "", "", "", fmt.Errorf("missing annotation '%s'", AnnotationDexStaticClientRedirectURI)
	}

	static_client_secret, ok := ann[AnnotationDexStaticClientSecret]
	if !ok {
		return "", "", "", "", fmt.Errorf("missing annotation '%s'", AnnotationDexStaticClientSecret)
	}

	return static_client_id, static_client_name, static_client_redirect_uri, static_client_secret, nil
}

// Handle Client creation on Ingress event
func (c *IngressClient) OnAdd(obj interface{}) {

	kind := "Ingress"

	o, ok := obj.(*v1beta1.Ingress)
	if !ok {
		log.Warnf("Got an unexpected, unsupported, object. Not an Ingress")
		return
	}

	log.Infof("Checking %s for client creation '%s' from namespace '%s' ...", kind, o.Name, o.Namespace)

	static_client_id, static_client_name, static_client_redirect_uri, static_client_secret, err := extractAnnotations(o.GetAnnotations())
	if err != nil {
		log.Infof("Ignoring %s '%s' from namespace '%s' - %s", kind, o.Name, o.Namespace, err)
		return
	}

	addDexStaticClient(c.dexClient, kind, o.Name, o.Namespace, static_client_id, static_client_name, static_client_redirect_uri, static_client_secret)
}

// Handle Ingress update event
func (c *IngressClient) OnUpdate(oldObj, newObj interface{}) {
	c.OnDelete(oldObj)
	c.OnAdd(newObj)
}

// Handle Ingress deletion event
func (c *IngressClient) OnDelete(obj interface{}) {
	kind := "Ingress"

	o, ok := obj.(*v1beta1.Ingress)
	if !ok {
		return
	}

	log.Debugf("Checking %s for client deletion for '%s' from namespace '%s' ...", kind, o.Name, o.Namespace)

	static_client_id, ok := o.GetAnnotations()[AnnotationDexStaticClientId]
	if !ok {
		log.Debugf("Ignoring %s '%s' from namespace '%s' - missing %s", kind, o.Name, o.Namespace, AnnotationDexStaticClientId)
		return
	}

	deleteDexStaticClient(c.dexClient, kind, o.Name, o.Namespace, static_client_id)
}

// Handle Client creation on ConfigMap event
func (c *ConfigMapClient) OnAdd(obj interface{}) {

	kind := "ConfigMap"

	o, ok := obj.(*v1.ConfigMap)
	if !ok {
		log.Warnf("Got an unexpected, unsupported, object. Not an ConfigMap")
		return
	}

	log.Infof("Checking %s for client creation '%s' from namespace '%s' ...", kind, o.Name, o.Namespace)

	static_client_id, static_client_name, static_client_redirect_uri, static_client_secret, err := extractAnnotations(o.GetAnnotations())
	if err != nil {
		log.Infof("Ignoring %s '%s' from namespace '%s' - %s", kind, o.Name, o.Namespace, err)
		return
	}

	addDexStaticClient(c.dexClient, kind, o.Name, o.Namespace, static_client_id, static_client_name, static_client_redirect_uri, static_client_secret)
}

// Handle ConfigMap update event
func (c *ConfigMapClient) OnUpdate(oldObj, newObj interface{}) {
	c.OnDelete(oldObj)
	c.OnAdd(newObj)
}

// Handle ConfigMap deletion event
func (c *ConfigMapClient) OnDelete(obj interface{}) {
	kind := "ConfigMap"

	o, ok := obj.(*v1.ConfigMap)
	if !ok {
		return
	}

	log.Debugf("Checking %s for client deletion for '%s' from namespace '%s' ...", kind, o.Name, o.Namespace)

	static_client_id, ok := o.GetAnnotations()[AnnotationDexStaticClientId]
	if !ok {
		log.Debugf("Ignoring %s '%s' from namespace '%s' - missing %s", kind, o.Name, o.Namespace, AnnotationDexStaticClientId)
		return
	}

	deleteDexStaticClient(c.dexClient, kind, o.Name, o.Namespace, static_client_id)
}

// Handle Client creation on Secret event
func (c *SecretClient) OnAdd(obj interface{}) {

	kind := "Secret"

	o, ok := obj.(*v1.Secret)
	if !ok {
		log.Warnf("Got an unexpected, unsupported, object. Not an Secret")
		return
	}

	log.Infof("Checking %s for client creation '%s' from namespace '%s' ...", kind, o.Name, o.Namespace)

	static_client_id, static_client_name, static_client_redirect_uri, static_client_secret, err := extractAnnotations(o.GetAnnotations())
	if err != nil {
		log.Infof("Ignoring %s '%s' from namespace '%s' - %s", kind, o.Name, o.Namespace, err)
		return
	}

	addDexStaticClient(c.dexClient, kind, o.Name, o.Namespace, static_client_id, static_client_name, static_client_redirect_uri, static_client_secret)
}

// Handle Secret update event
func (c *SecretClient) OnUpdate(oldObj, newObj interface{}) {
	c.OnDelete(oldObj)
	c.OnAdd(newObj)
}

// Handle Secret deletion event
func (c *SecretClient) OnDelete(obj interface{}) {
	kind := "Secret"

	o, ok := obj.(*v1.Secret)
	if !ok {
		return
	}

	log.Debugf("Checking %s for client deletion for '%s' from namespace '%s' ...", kind, o.Name, o.Namespace)

	static_client_id, ok := o.GetAnnotations()[AnnotationDexStaticClientId]
	if !ok {
		log.Debugf("Ignoring %s '%s' from namespace '%s' - missing %s", kind, o.Name, o.Namespace, AnnotationDexStaticClientId)
		return
	}

	deleteDexStaticClient(c.dexClient, kind, o.Name, o.Namespace, static_client_id)
}

// Return a new k8s client based on local or in-cluster configuration
func newClient(kubeconfig string, inCluster bool) *kubernetes.Clientset {
	var err error
	var config *rest.Config
	if kubeconfig != "" && !inCluster {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		exitOnError(err)
	} else {
		config, err = rest.InClusterConfig()
		exitOnError(err)
	}

	client, err := kubernetes.NewForConfig(config)
	exitOnError(err)
	return client
}

// Watch all ingresses in all namespaces and add event-handlers
func watchIngress(client *kubernetes.Clientset, rs ...cache.ResourceEventHandler) cache.SharedInformer {
	lw := cache.NewListWatchFromClient(client.ExtensionsV1beta1().RESTClient(), "ingresses", v1.NamespaceAll, fields.Everything())
	sw := cache.NewSharedInformer(lw, new(v1beta1.Ingress), SyncPeriodInMinutes*time.Minute)
	for _, r := range rs {
		sw.AddEventHandler(r)
	}
	return sw
}

// Watch all configmaps matching label selector in all namespaces and add event-handlers
func watchConfigMaps(client *kubernetes.Clientset, rs ...cache.ResourceEventHandler) cache.SharedInformer {
	optionsModifier := func(options *metav1.ListOptions) {
		options.LabelSelector = configMapSecretsSelectorLabels
	}

	lw := cache.NewFilteredListWatchFromClient(client.CoreV1().RESTClient(), "configmaps", v1.NamespaceAll, optionsModifier)
	sw := cache.NewSharedInformer(lw, new(v1.ConfigMap), SyncPeriodInMinutes*time.Minute)
	for _, r := range rs {
		sw.AddEventHandler(r)
	}
	return sw
}

// Watch all secrets matching label selector in all namespaces and add event-handlers
func watchSecrets(client *kubernetes.Clientset, rs ...cache.ResourceEventHandler) cache.SharedInformer {
	optionsModifier := func(options *metav1.ListOptions) {
		options.LabelSelector = configMapSecretsSelectorLabels
	}

	lw := cache.NewFilteredListWatchFromClient(client.CoreV1().RESTClient(), "secrets", v1.NamespaceAll, optionsModifier)
	sw := cache.NewSharedInformer(lw, new(v1.Secret), SyncPeriodInMinutes*time.Minute)
	for _, r := range rs {
		sw.AddEventHandler(r)
	}
	return sw
}

// Helper to print errors and exit
func exitOnError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	flag.Parse()
}

// Define usage and start the app
func main() {
	// Initialize logger.
	app := kingpin.New("app", "Create Dex client based of Ingress")

	serve := app.Command("serve", "Run it")
	inCluster := serve.Flag("incluster", "use in cluster configuration.").Bool()
	kubeconfig := serve.Flag("kubeconfig", "path to kubeconfig (if not in running inside a cluster)").Default(filepath.Join(os.Getenv("HOME"), ".kube", "config")).String()
	dexGrpcService := serve.Flag("dex-grpc-address", "dex grpc address").Default("127.0.0.1:5557").String()
	logJson := serve.Flag("log-json", "set log formatter to json").Bool()

	caCrtPath := serve.Flag("ca-crt", "CA certificate path").String()
	clientCrtPath := serve.Flag("client-crt", "client certificate path").String()
	clientKeyPath := serve.Flag("client-key", "client key path").String()

	enableIngressController := serve.Flag("ingress-controller", "Enable the ingress controller loop").Default("true").Bool()
	enableConfigmapController := serve.Flag("configmap-controller", "Enable the configmap controller loop").Default("false").Bool()
	enableSecretController := serve.Flag("secret-controller", "Enable the secret controller loop").Default("false").Bool()

	args := os.Args[1:]
	switch kingpin.MustParse(app.Parse(args)) {
	default:
		app.Usage(args)
		os.Exit(2)
	case serve.FullCommand():

		log.Infof("args: %v", args)

		if *logJson {
			log.SetFormatter(&log.JSONFormatter{})
		}

		client := newClient(*kubeconfig, *inCluster)
		dexClient := newDexClient(*dexGrpcService, *caCrtPath, *clientCrtPath, *clientKeyPath)

		r := http.NewServeMux()
		r.Handle("/healthz", healthcheck.Handler(
			healthcheck.WithChecker(
				"local", healthcheck.CheckerFunc(
					func(ctx context.Context) error {
						// always pass if we get this far
						return nil
					},
				),
			),
		))

		r.Handle("/readiness", healthcheck.Handler(
			healthcheck.WithChecker(
				"dex-grpc", healthcheck.CheckerFunc(
					func(ctx context.Context) error {
						// ping dex
						req := &api.VersionReq{}
						_, err := dexClient.GetVersion(context.TODO(), req)
						return err
					},
				),
			),
		))

		go func() {
			http.ListenAndServe(":8080", r)
		}()

		if *enableIngressController {
			c_ing := NewIngressClient(dexClient)
			log.Infof("Starting Ingress controller loop")
			wi := watchIngress(client, c_ing)
			go wi.Run(nil)
		}

		if *enableConfigmapController {
			c_cm := NewConfigMapClient(dexClient)
			log.Infof("Starting Configmap controller loop")
			wc := watchConfigMaps(client, c_cm)
			go wc.Run(nil)
		}

		if *enableSecretController {
			c_sec := NewSecretClient(dexClient)
			log.Infof("Starting Secret controller loop")
			sc := watchSecrets(client, c_sec)
			go sc.Run(nil)
		}

		// Wait forever
		select {}
	}
}
