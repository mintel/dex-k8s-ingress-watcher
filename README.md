# dex-k8s-ingress-watcher

Monitor kubernetes ingresses and modify the `staticClients` list in a dex 
configuration via gRPC.

This is to get around the issue that Dex does not support wildcards in it's
redirectURI option.


## Building

```
make build
```

## Running

If run as a binary outside of cluster, should use $HOME/.kube/config, else uses
in-cluster configuration.

```
./bin/dex-k8s-ingress-watcher serve --dex-grpc-address localhost:5557                      
```

# Ingress Configuration

`dex-k8s-ingress-watcher` monitors for the creation and deletion of Ingress events
in your kubernetes cluster.

The event-handlers check for specific annotations, which are used to pass on information
related to the creation of `staticClient` entries in Dex via gRPC.

## Annotations

```
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  annotations:
    mintel.com/dex-k8s-ingress-watcher-client-id: my-app
    mintel.com/dex-k8s-ingress-watcher-client-name: My Application
    mintel.com/dex-k8s-ingress-watcher-redirect-uri: https://myapp.example.com/oauth/callback

```

Such an annotation would generate in Dex the following `staticClient`

```
staticClients:
- id: my-app
  name: My Application
  redirectURIs:
  - 'https://myapp.example.com/oauth/callback'
```

## Client application configuration

We typically run an OpenID proxy service as a sidecar against applications we want to protect via Dex.

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
- Ingress points at this
- Proxy has an `upstream-url` which is the application you want to product (running on same host, different port)
- `keycloak` lets you protect by `resources=uri` option, restricting by groups returned by Dex if required
- `client-secret` is hard-coded in this app
- `client-id` must match the `mintel.com/dex-k8s-ingress-watcher-client-id` annotation

May want to look at injecting this automatically oneday using k8s webhooks:

- https://github.com/istio/istio/tree/master/pilot/pkg/kube/inject



# Issues

- Secret is hard-coded, and looks like it's required
- Not handling onUpdate event (not sure it's required)
- TODO: Re-structure code
- TODO: Test TLS support
- TODO: Set --verbose mode 
-
