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

# Issues

- Secret is hard-coded. Do we even need it?
- Verbosity
- Not handling onUpdate event (not sure it's required)
- No TLS support yet