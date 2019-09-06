# dex-k8s-ingress-watcher

Monitor kubernetes ingresses, configmap and Secrets to modify the `staticClients` list in a dex 
configuration via gRPC.

When a new Resource is spotted, this application will use the annotations 
defined in the resource to add a new `staticClient` entry in Dex.

by default only the `Ingress watcher` is started since this is the most common pattern, optionally 
a `configMap` and/or `secret` can be started to allow creating Dex Clients for resources outside the cluster or 
not connected to an ingress 

The use of a `secret` can also be useful if , by using `SealedSecrets` or a similar operator, you want to keep the client secret really secret

## Building

```
make build
```

## Running

If run as a binary outside of cluster, should use $HOME/.kube/config, else uses
in-cluster configuration.

_default with only ingress watcher_
```
./bin/dex-k8s-ingress-watcher serve --dex-grpc-address localhost:5557                      
```

_with configmap and secret watcher_
```
./bin/dex-k8s-ingress-watcher serve --dex-grpc-address --ingress-controller --configmap-controller --secret-controller localhost:5557
```

_disable ingress watcher_
```
./bin/dex-k8s-ingress-watcher serve --dex-grpc-address --no-ingress-controller --configmap-controller --secret-controller localhost:5557
```

### RBAC Notes

The clusterrole in the [example deployment directory](https://github.com/mintel/dex-k8s-ingress-watcher/blob/master/hack/deployment/clusterrole.yaml) is configured to support all controllers _( Ingress, ConfigMaps and Secrets )_

Make sure to remove the ones that you don't plan to use to limit access to those resources if not required, this is particularly true for _Secrets_

# Resource Configuration

`dex-k8s-ingress-watcher` monitors for the creation and deletion of Ingress, ConfigMap and Secrets events
in your kubernetes cluster.

* all **Ingresses** in **all-namespaces** are watched , if the required annotations are present in the resource then the _Dex client_ is created/deleted
* **ConfigMap** and **Secrets** in **all-namespaces** are watched only if they have a **specific label** applied to them, if the required annotations are present in the resource then the _Dex client_ is created/deleted<br>
  _mintel.com/dex-k8s-ingress-watcher: enabled_<br>
	This is done to avoid watching a big number of secrets / configmaps where only a very small subset will be used

The event-handlers check for specific annotations, which are used to pass on information
related to the creation of `staticClient` entries in Dex via gRPC.

## Annotations

Annotations are the same for every type of resource

```
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  annotations:
    mintel.com/dex-k8s-ingress-watcher-client-id: my-app
    mintel.com/dex-k8s-ingress-watcher-client-name: My Application
    mintel.com/dex-k8s-ingress-watcher-secret: a-secret
    mintel.com/dex-k8s-ingress-watcher-redirect-uri: https://myapp.example.com/oauth/callback
```

Such an annotation would generate in Dex the following `staticClient`

```
staticClients:
- id: my-app
  name: My Application
  secret: a-secret
  redirectURIs:
  - 'https://myapp.example.com/oauth/callback'
```

Note that `mintel.com/dex-k8s-ingress-watcher-client-name` is optional ( default to the same as _client-id_) , and the rest are required.

## Running in Kubernetes

An example deployment of DEX with the ingress watcher can be found in the [deployment directory](https://github.com/mintel/dex-k8s-ingress-watcher/blob/master/hack/deployment/)

**This is just an example and does not want to be production ready** 
* It does not run DEX on SSL
* It grants access to the serviceaccount to all configmaps and secrets on the cluster , this might not be what you want

We run this application as a sidecar to Dex itself - that way it can talk over gRPC via 
localhost.

In this example, Dex is running on `127.0.0.1` with gRPC exposed on port `5557`.

Example sidecar configuration:

```
  - name: dex-k8s-ingress-watcher
    command:
    - /app/bin/dex-k8s-ingress-watcher
    - serve
    - --incluster
    - --ingress-controller
    - --configmap-controller
    - --secret-controller
    - --dex-grpc-address
    - 127.0.0.1:5557
    image: mintel/dex-k8s-ingress-watcher:latest
    imagePullPolicy: IfNotPresent
    resources:
      limits:
        cpu: 50m
        memory: 64Mi
      requests:
        cpu: 20m
        memory: 32Mi
```

## Authenticated client application configuration

An application wishing to authenticate via Dex can typically run an OpenID proxy service as a
sidecar container.

A good example is [keycloak-proxy](https://github.com/gambol99/keycloak-proxy)

Example sidecar configuration:

```
- name: proxy
  image: quay.io/gambol99/keycloak-proxy:v2.1.1
  imagePullPolicy: Always
  resources:
    limits:
      cpu: 100m
      memory: 128Mi 
    requests:
      cpu: 50m
      memory: 64Mi
  args:
    - --verbose=true
    - --listen=:3000
    - --upstream-url=http://0.0.0.0:8000
    - --discovery-url=https://dex.example.com/.well-known/openid-configuration
    - --client-id=my-app
    - --skip-upstream-tls-verify
    - --redirection-url=https://myapp.example.com
    - --secure-cookie=false
    - --client-secret=a-secret
    - --enable-authorization-header
    - --skip-openid-provider-tls-verify
    - --add-claims=groups
    - --scopes=groups
    - --add-claims=groups
    - --resources=uri=/*
```

Key points to note:
- Your Ingress and Service must point at the keycloak proxy port, i.e `3000` in this example
- Proxy has an `upstream-url` which is the application you want to product (running on same host, different port)
- `client-secret` must match the `mintel.com/dex-k8s-ingress-watcher-secret` annotation
- `client-id` must match the `mintel.com/dex-k8s-ingress-watcher-client-id` annotation
- `keycloak` lets you protect by `resources=uri` option, restricting by groups returned by Dex if required

May want to look at injecting this automatically oneday using k8s webhooks:

- https://github.com/istio/istio/tree/master/pilot/pkg/kube/inject

# Limitations

- Does not support multiple host definitions in the Ingress, as the annotation only supports a single callback uri.

# TODO

- Tidy up structure of code
- Test TLS support
- Set --verbose mode 
