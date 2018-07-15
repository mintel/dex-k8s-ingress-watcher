//
// An application which watches for Ingresses and configures Dex clients via 
// gRPC dynamically, based on Ingress annotations.
//
// work-in-progress
//
// todo:
//  module structure
//  tests
//  dex-client impl.
//  certs for dex
//  annotation parsing
//  in-cluster yaml and image-building
//
package main

import (
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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

    "github.com/coreos/dex/api"
	"google.golang.org/grpc"
	_"google.golang.org/grpc/credentials"
)

type DexK8sDynamicClientsApp struct {
	logger logrus.FieldLogger
    dexClient api.DexClient
}

/*
func extractAnnotationDetails(obj interface{}) {}

https://github.com/coreos/dex/blob/master/examples/grpc-client/client.go
func addDexClient() {}
func removeDexClient() {}
*/

func newDexClient(hostAndPort string) (api.DexClient, error) {
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
	conn, err := grpc.Dial(hostAndPort)
	if err != nil {
		return nil, fmt.Errorf("dail: %v", err)
	}
	return api.NewDexClient(conn), nil
}

const (
	// IngressKey picks a specific "class" for the Ingress.
	IngressAnnotationStaticClient = "mintel.com/dex-static-client"
)


func checkIngressHasAnnotions(ing *v1beta1.Ingress, c *DexK8sDynamicClientsApp) (bool) {
	if ing.GetAnnotations() == nil {
		return false
	}

    _, ok := ing.GetAnnotations()[IngressAnnotationStaticClient]
    if !ok {
	    c.logger.Infof("Annotation not found in ingress %s", ing)
        return false
    }
    return true
}

// GetDomains returns the list of hosts associated with rules 
func getDomains(ingress *v1beta1.Ingress) []string {
	hosts := []string{}
	for _, rule := range ingress.Spec.Rules {
	    hosts = append(hosts, rule.Host)
	}
	return hosts
}

func addIngressAsDexStaticClient(ing *v1beta1.Ingress, c *DexK8sDynamicClientsApp) {
    hosts := getDomains(ing)
    for _, host := range hosts {
        c.logger.Infof("Got Host %s", host)

        // Add host as static client to Dex
        addClientRedirectUriReq := &api.AddClientRedirectUriReq{
			Id: "dex-k8s-dynamic",
			RedirectUri: host,
		}

        _, err := c.dexClient.ClientAddRedirectUri(addClientRedirectUriReq)
		if err == nil {
			c.logger.Infof("redirect URI successfully added.\n")
		}

    }
}


// Return a new app.
func NewDexK8sDynamicClientsApp(logger logrus.FieldLogger, dexClient api.DexClient) *DexK8sDynamicClientsApp {
	return &DexK8sDynamicClientsApp{
		logger: logger,
        dexClient: dexClient,
	}
}

// Ingress event-handlers
func (c *DexK8sDynamicClientsApp) OnAdd(obj interface{}) {
	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		c.logger.Errorf("OnAdd endpoints received invalid obj; %T: %#v", ing, ing)
        return
	}

	c.logger.Infof("OnAdd got type %T: %#v", obj, obj)

    if checkIngressHasAnnotions(ing, c) {
        addIngressAsDexStaticClient(ing, c)
    }
}

func (c *DexK8sDynamicClientsApp) OnUpdate(oldObj, newObj interface{}) {
	switch newObj := newObj.(type) {
	case *v1beta1.Ingress:
		oldObj, ok := oldObj.(*v1beta1.Ingress)
		if !ok {
			c.logger.Errorf("OnUpdate endpoints %#v received invalid oldObj %T; %#v", newObj, oldObj, oldObj)
			return
		}
		c.logger.Infof("OnUpdate got type %T: %#v", newObj, newObj)
	default:
		c.logger.Errorf("OnUpdate unexpected type %T: %#v", newObj, newObj)
	}
}

func (c *DexK8sDynamicClientsApp) OnDelete(obj interface{}) {
	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		c.logger.Errorf("OnDelete endpoints received invalid obj; %T: %#v", ing, ing)
        return
	}
	c.logger.Infof("OnDelete got type %T: %#v", obj, obj)
}

func init() {
	flag.Parse()
}

func main() {
	// Initialize logger.
	log := logrus.StandardLogger()

	app := kingpin.New("app", "Create Dex client based of Ingress")

	serve := app.Command("serve", "Run it")
	inCluster := serve.Flag("incluster", "use in cluster configuration.").Bool()
	kubeconfig := serve.Flag("kubeconfig", "path to kubeconfig (if not in running inside a cluster)").Default(filepath.Join(os.Getenv("HOME"), ".kube", "config")).String()

	args := os.Args[1:]
	switch kingpin.MustParse(app.Parse(args)) {
	//default:
	//	app.Usage(args)
	//	os.Exit(2)
	//case serve.FullCommand():
	default:
		log.Infof("args: %v", args)

		client := newClient(*kubeconfig, *inCluster)
		logger := logrus.New().WithField("context", "app")

        dexClient, err := newDexClient("127.0.0.1:5557")
        if err != nil {
            logger.Infof("Darn cannot get dex %s", err)
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
