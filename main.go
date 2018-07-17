//
// An application which watches for Ingresses and configures Dex clients via
// gRPC dynamically, based on Ingress annotations.
//
// This is a helper tool to get around the fact that Dex does not support
// wildcards in redirect-uris: https://github.com/coreos/dex/issues/1261
//
//
//
// Example ingress in kubernetes:
//
// apiVersion: extensions/v1beta1
// kind: Ingress
// metadata:
//   annotations:
// 	   mintel.com/dex-static-client-id: my-app
//     mintel.com/dex-static-client-name: My Application
// 	   mintel.com/dex-redirect-uri: https://my-app.svc.example.com/oauth/callback
//
//
package main

import (
	"context"
	"flag"
	"fmt"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
	"os"
	"path/filepath"
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/coreos/dex/api"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/credentials"
)

// App struct
type DexK8sDynamicClientsApp struct {
	logger    logrus.FieldLogger
	dexClient api.DexClient
}

// Return a new Dex Client to perform gRPC calls with
func newDexClient(hostAndPort string) (api.DexClient, error) {

	//
	// TODO: Add TLS options
	//

	//func newDexClient(hostAndPort, caPath, clientCrt, clientKey string) (api.DexClient, error) {
	/*cPool := x509.NewCertPool()
	caCert, err := ioutil.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("invalid CA crt file: %s", caPath)
	}
	if cPool.AppendCertsFromPEM(caCert) != true {
		return nil, fmt.Errorf("failed to parse CA crt")
	}

	clientCert, err := tls.LoadX509KeyPair(clientCrt, clientKey)
	if err != nil {
		return nil, fmt.Errorf("invalid client crt file: %s", caPath)
	}

	clientTLSConfig := &tls.Config{
		RootCAs:      cPool,
		Certificates: []tls.Certificate{clientCert},
	}
	creds := credentials.NewTLS(clientTLSConfig)
	conn, err := grpc.Dial(hostAndPort, grpc.WithTransportCredentials(creds))
	*/
	//conn, err := grpc.Dial(hostAndPort)
	conn, err := grpc.Dial(hostAndPort, grpc.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("dex dial: %v", err)
	}
	return api.NewDexClient(conn), nil
}

const (
	// Define annotations we check for in the Ingress annotations
	// metadata
	IngressAnnotationDexStaticClientId          = "mintel.com/dex-static-client-id"
	IngressAnnotationDexStaticClientName        = "mintel.com/dex-static-client-name"
	IngressAnnotationDexStaticClientRedirectURI = "mintel.com/dex-redirect-uri"
)

const (
	DexStaticClientSecret = "a-secret"
)

// Add Dex StaticClient via gRPC
func (c *DexK8sDynamicClientsApp) addDexStaticClient(
	ing *v1beta1.Ingress,
	static_client_id string,
	static_client_name string,
	static_client_redirect_uri string) {

	c.logger.Infof("Registering Ingress '%s'\n\tClient ID: %s\n\tClient Name: %s\n\tRedirectURI: %s",
		ing.Name,
		static_client_id,
		static_client_name,
		static_client_redirect_uri)

	redirect_uris := []string{static_client_redirect_uri}

	req := &api.CreateClientReq{
		Client: &api.Client{
			Id:           static_client_id,
			Name:         static_client_name,
			Secret:       DexStaticClientSecret,
			RedirectUris: redirect_uris,
		},
	}

	if resp, err := c.dexClient.CreateClient(context.TODO(), req); err != nil {
		c.logger.Errorf("Dex gRPC: Failed creating oauth2 client for Ingress '%s': %v", ing.Name, err)
	} else {
		if resp.AlreadyExists {
			c.logger.Errorf("Dex gPRC: client already exists for Ingress '%s'", ing.Name)
		} else {
			c.logger.Infof("Dex gRPC: Successfully created client for Ingress '%s'", ing.Name)
		}
	}
}

// Delete Dex StaticClient via gRPC
func (c *DexK8sDynamicClientsApp) deleteDexStaticClient(
	ing *v1beta1.Ingress,
	static_client_id string) {

	c.logger.Infof("Deleting Ingress '%s'\n\tClient ID: %s", ing.Name, static_client_id)

	req := &api.DeleteClientReq{
		Id: static_client_id,
	}

	if resp, err := c.dexClient.DeleteClient(context.TODO(), req); err != nil {
		c.logger.Errorf("Dex gRPC: Failed deleting oauth2 client for Ingress '%s': %v", ing.Name, err)
	} else {
		if resp.NotFound {
			c.logger.Errorf("Dex gPRC: client '%s' could not be deleted for Ingress '%s' - not found '%s'", static_client_id, ing.Name)
		} else {
			c.logger.Infof("Dex gRPC: Successfully deleted client for Ingress '%s'", ing.Name)
		}
	}
}

// Return a new app.
func NewDexK8sDynamicClientsApp(logger logrus.FieldLogger, dexClient api.DexClient) *DexK8sDynamicClientsApp {
	return &DexK8sDynamicClientsApp{
		logger:    logger,
		dexClient: dexClient,
	}
}

// Handle Ingress creation event
func (c *DexK8sDynamicClientsApp) OnAdd(obj interface{}) {

	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		return
	}

	c.logger.Debugf("Checking Ingress creation '%s'...", ing.Name)
	static_client_id, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientId]
	if !ok {
		c.logger.Debugf("Ignoring Ingress '%s' - missing %s ", ing.Name, IngressAnnotationDexStaticClientId)
		return
	}

	static_client_name, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientName]

	if !ok {
		c.logger.Debugf("Ignoring Ingress '%s' - missing %s ", ing.Name, IngressAnnotationDexStaticClientName)
		return
	}

	static_client_redirect_uri, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientRedirectURI]

	if !ok {
		c.logger.Debugf("Ignoring Ingress '%s' - missing %s ", ing.Name, IngressAnnotationDexStaticClientRedirectURI)
		return
	}

	c.addDexStaticClient(ing, static_client_id, static_client_name, static_client_redirect_uri)
}

// Handle Ingress update event
// TODO: Confirm if required
func (c *DexK8sDynamicClientsApp) OnUpdate(oldObj, newObj interface{}) {}

// Handle Ingress deletion event
func (c *DexK8sDynamicClientsApp) OnDelete(obj interface{}) {

	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		return
	}

	c.logger.Debugf("Checking Ingress deletion for '%s'...", ing.Name)

	static_client_id, ok := ing.GetAnnotations()[IngressAnnotationDexStaticClientId]
	if !ok {
		c.logger.Debugf("Ignoring Ingress '%s' - missing %s ", ing.Name, IngressAnnotationDexStaticClientId)
		return
	}

	c.deleteDexStaticClient(ing, static_client_id)
}

func init() {
	flag.Parse()
}

// Define usage and start the app
func main() {
	// Initialize logger.
	log := logrus.StandardLogger()

	app := kingpin.New("app", "Create Dex client based of Ingress")

	serve := app.Command("serve", "Run it")
	inCluster := serve.Flag("incluster", "use in cluster configuration.").Bool()
	kubeconfig := serve.Flag("kubeconfig", "path to kubeconfig (if not in running inside a cluster)").Default(filepath.Join(os.Getenv("HOME"), ".kube", "config")).String()
	dex_grpc_service := serve.Flag("dex-grpc-address", "dex grpc address").Default("127.0.0.1.5557").String()

	args := os.Args[1:]
	switch kingpin.MustParse(app.Parse(args)) {
	default:
		app.Usage(args)
		os.Exit(2)
	case serve.FullCommand():
		log.Infof("args: %v", args)

		client := newClient(*kubeconfig, *inCluster)
		logger := logrus.New().WithField("context", "app")

		dexClient, err := newDexClient(*dex_grpc_service)
		if err != nil {
			logger.Infof("Cannot contact Dex gRPC service at %s: %s", dex_grpc_service, err)
		}

		c := NewDexK8sDynamicClientsApp(logger, dexClient)
		w := watchIngress(client, c)
		w.Run(nil)
	}
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
