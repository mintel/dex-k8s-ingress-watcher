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

    _"github.com/coreos/dex/api"
	_"google.golang.org/grpc"
	_"google.golang.org/grpc/credentials"
)

type DexK8sDynamicClientsApp struct {
	logger logrus.FieldLogger
}

/*
func checkIngressHasAnnotions(obj interface{}) {}
func extractAnnotationDetails(obj interface{}) {}

https://github.com/coreos/dex/blob/master/examples/grpc-client/client.go
func addDexClient() {}
func removeDexClient() {}

func newDexClient() {}
*/

// Return a new app.
func NewDexK8sDynamicClientsApp(logger logrus.FieldLogger) *DexK8sDynamicClientsApp {
	return &DexK8sDynamicClientsApp{
		logger: logger,
	}
}

// Ingress event-handlers
func (c *DexK8sDynamicClientsApp) OnAdd(obj interface{}) {
	ing, ok := obj.(*v1beta1.Ingress)
	if !ok {
		c.logger.Errorf("OnAdd type %T: %#v", ing, ing)
	}
	c.logger.Infof("OnDelete unexpected type %T: %#v", obj, obj)
}

func (c *DexK8sDynamicClientsApp) OnUpdate(oldObj, newObj interface{}) {
	switch newObj := newObj.(type) {
	case *v1beta1.Ingress:
		oldObj, ok := oldObj.(*v1beta1.Ingress)
		if !ok {
			c.logger.Errorf("OnUpdate endpoints %#v received invalid oldObj %T; %#v", newObj, oldObj, oldObj)
			return
		}
		c.logger.Infof("OnDelete got type %T: %#v", newObj, newObj)
	default:
		c.logger.Errorf("OnUpdate unexpected type %T: %#v", newObj, newObj)
	}
}

func (c *DexK8sDynamicClientsApp) OnDelete(obj interface{}) {
	switch obj := obj.(type) {
	case *v1beta1.Ingress:
		c.logger.Infof("OnDeleteunexpected type %T: %#v", obj, obj)
	default:
		c.logger.Errorf("OnDelete unexpected type %T: %#v", obj, obj)
	}
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

		c := NewDexK8sDynamicClientsApp(logger)
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
