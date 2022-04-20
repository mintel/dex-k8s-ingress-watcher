//
// An application which watches for Ingresses and configures Dex clients via
// gRPC dynamically, based on Ingress annotations.
//
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/coreos/dex/api"
	"github.com/etherlabsio/healthcheck"
	log "github.com/sirupsen/logrus"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	v1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	netv1 "k8s.io/api/networking/v1"
	netv1beta1 "k8s.io/api/networking/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
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

		if !cPool.AppendCertsFromPEM(caCert) {
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
		conn, err := grpc.Dial(grpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
func deleteDexStaticClient(c api.DexClient, kind string, name string, namespace string, static_client_id string) {

	log.Infof("Deleting %s '%s' with static client '%s'", kind, name, static_client_id)

	req := &api.DeleteClientReq{
		Id: static_client_id,
	}

	resp, err := c.DeleteClient(context.TODO(), req)
	exitOnError(err)
	if resp.NotFound {
		log.Errorf("Dex gPRC: client '%s' could not be deleted for %s '%s' from namespace '%s' - not found", static_client_id, kind, name, namespace)
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

	const kind = "Ingress"

	var (
		name                       string
		namespace                  string
		static_client_id           string
		static_client_name         string
		static_client_redirect_uri string
		static_client_secret       string
		err                        error
	)
	switch o := obj.(type) {
	case *extv1beta1.Ingress:
		name, namespace = o.Name, o.Namespace
		static_client_id, static_client_name, static_client_redirect_uri, static_client_secret, err = extractAnnotations(o.GetAnnotations())
	case *netv1beta1.Ingress:
		name, namespace = o.Name, o.Namespace
		static_client_id, static_client_name, static_client_redirect_uri, static_client_secret, err = extractAnnotations(o.GetAnnotations())
	case *netv1.Ingress:
		name, namespace = o.Name, o.Namespace
		static_client_id, static_client_name, static_client_redirect_uri, static_client_secret, err = extractAnnotations(o.GetAnnotations())
	default:
		log.Warnf("Got an unexpected, unsupported, object. Not an Ingress")
		return
	}
	if err != nil {
		log.Infof("Ignoring %s '%s' from namespace '%s' - %s", kind, name, namespace, err)
		return
	}
	log.Infof("Checking %s for client creation '%s' from namespace '%s' ...", kind, name, namespace)

	addDexStaticClient(c.dexClient, kind, name, namespace, static_client_id, static_client_name, static_client_redirect_uri, static_client_secret)
}

// Handle Ingress update event
func (c *IngressClient) OnUpdate(oldObj, newObj interface{}) {
	c.OnDelete(oldObj)
	c.OnAdd(newObj)
}

// Handle Ingress deletion event
func (c *IngressClient) OnDelete(obj interface{}) {
	const kind = "Ingress"

	var (
		name             string
		namespace        string
		static_client_id string
		ok               bool
	)
	switch o := obj.(type) {
	case *extv1beta1.Ingress:
		name, namespace = o.Name, o.Namespace
		static_client_id, ok = o.GetAnnotations()[AnnotationDexStaticClientId]
	case *netv1beta1.Ingress:
		name, namespace = o.Name, o.Namespace
		static_client_id, ok = o.GetAnnotations()[AnnotationDexStaticClientId]
	case *netv1.Ingress:
		name, namespace = o.Name, o.Namespace
		static_client_id, ok = o.GetAnnotations()[AnnotationDexStaticClientId]
	default:
		log.Warnf("Got an unexpected, unsupported, object. Not an Ingress")
		return
	}
	log.Debugf("Checking %s for client deletion for '%s' from namespace '%s' ...", kind, name, namespace)
	if !ok {
		log.Debugf("Ignoring %s '%s' from namespace '%s' - missing %s", kind, name, namespace, AnnotationDexStaticClientId)
		return
	}

	deleteDexStaticClient(c.dexClient, kind, name, namespace, static_client_id)
}

// Handle Client creation on ConfigMap event
func (c *ConfigMapClient) OnAdd(obj interface{}) {

	const kind = "ConfigMap"

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
	const kind = "ConfigMap"

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

	const kind = "Secret"

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
	const kind = "Secret"

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

// Watch all extensions/v1beta1 Ingresses in all namespaces and add event-handlers
func watchExtensionsV1Beta1Ingress(client *kubernetes.Clientset, rs ...cache.ResourceEventHandler) cache.SharedInformer {
	lw := cache.NewListWatchFromClient(client.ExtensionsV1beta1().RESTClient(), "ingresses", v1.NamespaceAll, fields.Everything())
	sw := cache.NewSharedInformer(lw, new(extv1beta1.Ingress), SyncPeriodInMinutes*time.Minute)
	for _, r := range rs {
		sw.AddEventHandler(r)
	}
	return sw
}

// Watch all networking/v1beta1 Ingresses in all namespaces and add event-handlers
func watchNetworkingV1Beta1Ingress(client *kubernetes.Clientset, rs ...cache.ResourceEventHandler) cache.SharedInformer {
	lw := cache.NewListWatchFromClient(client.NetworkingV1beta1().RESTClient(), "ingresses", v1.NamespaceAll, fields.Everything())
	sw := cache.NewSharedInformer(lw, new(netv1beta1.Ingress), SyncPeriodInMinutes*time.Minute)
	for _, r := range rs {
		sw.AddEventHandler(r)
	}
	return sw
}

// Watch all networking/v1 Ingresses in all namespaces and add event-handlers
func watchNetworkingV1Ingress(client *kubernetes.Clientset, rs ...cache.ResourceEventHandler) cache.SharedInformer {
	lw := cache.NewListWatchFromClient(client.NetworkingV1().RESTClient(), "ingresses", v1.NamespaceAll, fields.Everything())
	sw := cache.NewSharedInformer(lw, new(netv1.Ingress), SyncPeriodInMinutes*time.Minute)
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
		log.Fatal(err)
	}
}

// CLI flags
var CLI struct {
	Serve struct {
		InCluster      bool   `name:"incluster" help:"use in cluster configuration."`
		KubeConfig     string `name:"kubeconfig" type:"path" default:"~/.kube/config" help:"path to kubeconfig (if not in running inside a cluster)"`
		DexGrpcService string `name:"dex-grpc-address" default:"127.0.0.1:5557" help:"dex grpc address"`
		LogJson        bool   `name:"log-json" help:"set log formatter to json"`

		CACrtPath     string `name:"ca-crt" type:"path" help:"CA certificate path"`
		ClientCrtPath string `name:"client-crt" type:"path" help:"client certificate path"`
		ClientKeyPath string `name:"client-key" type:"path" help:"client key path"`

		EnableIngressController   bool `name:"ingress-controller" negatable:"" default:"true" help:"Enable the controller loop for ingresses"`
		EnableConfigmapController bool `name:"configmap-controller" negatable:"" default:"false" help:"Enable the configmap controller loop"`
		EnableSecretController    bool `name:"secret-controller" negatable:"" default:"false" help:"Enable the secret controller loop"`
	} `cmd:"serve" help:"Run it"`
}

// Define usage and start the app
func main() {
	ctx := kong.Parse(&CLI)

	args := os.Args[1:]
	switch ctx.Command() {
	default:
		if err := ctx.PrintUsage(true); err != nil {
			panic(err)
		}
		os.Exit(2)
	case "serve":

		log.Infof("args: %v", args)

		if CLI.Serve.LogJson {
			log.SetFormatter(&log.JSONFormatter{})
		}

		client := newClient(CLI.Serve.KubeConfig, CLI.Serve.InCluster)
		dexClient := newDexClient(CLI.Serve.DexGrpcService, CLI.Serve.CACrtPath, CLI.Serve.ClientCrtPath, CLI.Serve.ClientKeyPath)

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
			exitOnError(http.ListenAndServe(":8080", r))
		}()

		if CLI.Serve.EnableIngressController {
			c_ing := NewIngressClient(dexClient)

			group, err := client.ServerResourcesForGroupVersion("networking.k8s.io/v1")
			if !errors.IsNotFound(err) {
				exitOnError(err)
				for _, resource := range group.APIResources {
					if resource.Kind == "Ingress" {
						log.Infof("Starting controller loop for networking/v1 Ingress")
						wi := watchNetworkingV1Ingress(client, c_ing)
						go wi.Run(nil)
						break
					}
				}
			}

			group, err = client.ServerResourcesForGroupVersion("networking.k8s.io/v1beta")
			if !errors.IsNotFound(err) {
				exitOnError(err)
				for _, resource := range group.APIResources {
					if resource.Kind == "Ingress" {
						log.Infof("Starting controller loop for networking/v1beta1 Ingress")
						wi := watchNetworkingV1Beta1Ingress(client, c_ing)
						go wi.Run(nil)
						break
					}
				}
			}

			group, err = client.ServerResourcesForGroupVersion("extensions/v1beta1")
			if !errors.IsNotFound(err) {
				exitOnError(err)
				for _, resource := range group.APIResources {
					if resource.Kind == "Ingress" {
						log.Infof("Starting controller loop for  Ingress")
						wi := watchExtensionsV1Beta1Ingress(client, c_ing)
						go wi.Run(nil)
						break
					}
				}
			}
		}

		if CLI.Serve.EnableConfigmapController {
			c_cm := NewConfigMapClient(dexClient)
			log.Infof("Starting controller loop for ConfigMap")
			wc := watchConfigMaps(client, c_cm)
			go wc.Run(nil)
		}

		if CLI.Serve.EnableSecretController {
			c_sec := NewSecretClient(dexClient)
			log.Infof("Starting controller loop for Secret")
			sc := watchSecrets(client, c_sec)
			go sc.Run(nil)
		}

		// Wait forever
		select {}
	}
}
