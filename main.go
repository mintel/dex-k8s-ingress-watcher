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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/coreos/dex/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	log "github.com/sirupsen/logrus"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
)

// App struct
type DexK8sDynamicClientsApp struct {
	dexClient api.DexClient
}

const (
	// Define annotations we check for in the Ingress annotations
	// metadata
	IngressAnnotationDexStaticClientId          = "mintel.com/dex-k8s-ingress-watcher-client-id"
	IngressAnnotationDexStaticClientName        = "mintel.com/dex-k8s-ingress-watcher-client-name"
	IngressAnnotationDexStaticClientRedirectURI = "mintel.com/dex-k8s-ingress-watcher-redirect-uri"
	IngressAnnotationDexStaticClientSecret      = "mintel.com/dex-k8s-ingress-watcher-secret"
)

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
func (c *DexK8sDynamicClientsApp) addDexStaticClient(
	ing *v1beta1.Ingress,
	static_client_id string,
	static_client_name string,
	static_client_redirect_uri string,
	static_client_secret string) {

	log.Infof("Registering Ingress '%s' with static client '%s' at callback '%s'",
		ing.Name,
		static_client_id,
		static_client_redirect_uri)

	redirect_uris := []string{static_client_redirect_uri}

	req := &api.CreateClientReq{
		Client: &api.Client{
			Id:           static_client_id,
			Name:         static_client_name,
			Secret:       static_client_secret,
			RedirectUris: redirect_uris,
		},
	}

	resp, err := c.dexClient.CreateClient(context.TODO(), req)
	exitOnError(err)
	if resp.AlreadyExists {
		log.Warnf("Dex gPRC: client already exists for Ingress '%s'", ing.Name)
	} else {
		log.Infof("Dex gRPC: Successfully created client for Ingress '%s'", ing.Name)
	}
}

// Delete Dex StaticClient via gRPC
func (c *DexK8sDynamicClientsApp) deleteDexStaticClient(
	ing *v1beta1.Ingress,
	static_client_id string) {

	log.Infof("Deleting Ingress '%s' with static client '%s'", ing.Name, static_client_id)

	req := &api.DeleteClientReq{
		Id: static_client_id,
	}

	resp, err := c.dexClient.DeleteClient(context.TODO(), req)
	exitOnError(err)
	if resp.NotFound {
		log.Errorf("Dex gPRC: client '%s' could not be deleted for Ingress '%s' - not found '%s'", static_client_id, ing.Name)
	} else {
		log.Infof("Dex gRPC: Successfully deleted client for Ingress '%s'", ing.Name)
	}
}

// Return a new app.
func NewDexK8sDynamicClientsApp(dexClient api.DexClient) *DexK8sDynamicClientsApp {
	return &DexK8sDynamicClientsApp{
		dexClient: dexClient,
	}
}

// Handle Ingress creation event
func (c *DexK8sDynamicClientsApp) OnAdd(obj interface{}) {

	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		log.Warnf("Got an unexpected object instead of an Ingress")
		return
	}

	log.Infof("Checking Ingress creation '%s'...", ing.Name)
	static_client_id, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientId]
	if !ok {
		log.Infof("Ignoring Ingress '%s' - missing %s", ing.Name, IngressAnnotationDexStaticClientId)
		return
	}

	static_client_name, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientName]

	if !ok {
		// Default to using the ID
		static_client_name = static_client_id
	}

	static_client_redirect_uri, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientRedirectURI]

	if !ok {
		log.Infof("Ignoring Ingress '%s' - missing %s", ing.Name, IngressAnnotationDexStaticClientRedirectURI)
		return
	}

	static_client_secret, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientSecret]

	if !ok {
		log.Infof("Ignoring Ingress '%s' - missing %s", ing.Name, IngressAnnotationDexStaticClientSecret)
		return
	}

	c.addDexStaticClient(ing, static_client_id, static_client_name, static_client_redirect_uri, static_client_secret)
}

// Handle Ingress update event
func (c *DexK8sDynamicClientsApp) OnUpdate(oldObj, newObj interface{}) {

	c.OnDelete(oldObj)
	c.OnAdd(newObj)
}

// Handle Ingress deletion event
func (c *DexK8sDynamicClientsApp) OnDelete(obj interface{}) {

	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		return
	}

	log.Debugf("Checking Ingress deletion for '%s'...", ing.Name)

	static_client_id, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientId]
	if !ok {
		log.Debugf("Ignoring Ingress '%s' - missing %s", ing.Name, IngressAnnotationDexStaticClientId)
		return
	}

	c.deleteDexStaticClient(ing, static_client_id)
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
	sw := cache.NewSharedInformer(lw, new(v1beta1.Ingress), 30*time.Minute)
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

		http.ListenAndServe(":8080", r)

		c := NewDexK8sDynamicClientsApp(dexClient)
		w := watchIngress(client, c)
		w.Run(nil)
	}

}
