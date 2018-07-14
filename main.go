package main

import (
	"os"
	"path/filepath"
	"time"

	v1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/spotahome/kooper/log"
	"github.com/spotahome/kooper/operator/controller"
	"github.com/spotahome/kooper/operator/handler"
	"github.com/spotahome/kooper/operator/retrieve"
)

func main() {
	// Initialize logger.
	log := &log.Std{}

	// Get k8s client.
	k8scfg, err := rest.InClusterConfig()
	if err != nil {
		// No in cluster? letr's try locally
		kubehome := filepath.Join(homedir.HomeDir(), ".kube", "config")
		k8scfg, err = clientcmd.BuildConfigFromFlags("", kubehome)
		if err != nil {
			log.Errorf("error loading kubernetes configuration: %s", err)
			os.Exit(1)
		}
	}
	k8scli, err := kubernetes.NewForConfig(k8scfg)
	if err != nil {
		log.Errorf("error creating kubernetes client: %s", err)
		os.Exit(1)
	}

	// Create our retriever so the controller knows how to get/listen for ingress events.
	retr := &retrieve.Resource{
		Object: &v1beta1.Ingress{},
		ListerWatcher: &cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				return k8scli.ExtensionsV1beta1().Ingresses("").List(options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return k8scli.ExtensionsV1beta1().Ingresses("").Watch(options)
			},
		},
	}

	// Our domain logic that will print every add/sync/update and delete event we .
	// Our domain logic that will print every add/sync/update and delete event we .
//DeleteFunc: func(obj interface{}) {
//			delIng := obj.(*extensions.Ingress)
//			if !isGCEIngress(delIng) && !isGCEMultiClusterIngress(delIng) {
//				glog.V(4).Infof("Ignoring delete for ingress %v based on annotation %v", delIng.Name, annotations.IngressClassKey)
//				return
//			}
	hand := &handler.HandlerFunc{
		AddFunc: func(obj runtime.Object) error {
            ingress := obj.(*v1beta1.Ingress)

            dex_redirect_uri, ok1 := ingress.GetAnnotations()["mintel.com/dex-redirect-uri"]
            dex_service_name, ok2 := ingress.GetAnnotations()["mintel.com/dex-service-name"]
            if ok1 && ok2 {
                log.Infof("Added Ingress '%s' - %s/%s", ingress, dex_redirect_uri, dex_service_name)
            }

			return nil
		},
		DeleteFunc: func(obj interface{}) {
			delIng := obj.(*v1beta1.Ingress)
			log.Infof("Ingress deleted: %s", delIng.Name)
		},
	}


	// Create the controller that will refresh every 30 seconds.
	ctrl := controller.NewSequential(30*time.Second, hand, retr, nil, log)

	// Start our controller.
	stopC := make(chan struct{})
	if err := ctrl.Run(stopC); err != nil {
		log.Errorf("error running controller: %s", err)
		os.Exit(1)
	}
	os.Exit(0)
}
