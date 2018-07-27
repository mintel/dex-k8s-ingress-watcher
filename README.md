# dex-k8s-ingress-watcher

Monitor kubernetes ingresses and modify the `staticClients` list in a dex 
configuration via gRPC.

This is to get around the issue that Dex does not support wildcards in it's
redirectURI option.

When a new Ingress is spotted, this application will use the annotations 
defined in the Ingress to add a new `staticClient` entry in Dex.

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

Note that `mintel.com/dex-k8s-ingress-watcher-client-name` is optional, and the rest are required.

## Running in Kubernetes

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

- Does not support multiple host definitions in the annotation, as we need to have a specific callbackup uri.

# TODO

- Handle onUpdate event
- Tidy up structure of code
- Test TLS support
- Set --verbose mode 
