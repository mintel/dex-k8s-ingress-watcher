# dex-k8s-dynamic-clients

Monitor kubernetes ingresses and modify the `staticClients` list in a dex 
configuration via gRPC.

This is to get around the issue that Dex does not support wildcards in it's
redirectURI option.

Resolves: https://gitlab.com/mintel/satoshi/infrastructure/terragrunt-satoshi-cluster-infrastructure/issues/30

We can opensource this if it's useful

# Design 

The simplest solution is:

- Run in a loop monitoring ingress 
- Match the annotation 'mintel.com/dex-k8s-dynamic-client: true' 
- Generate a gRPC call to the dex service to add the client info

## Future Design

- Create a controller to watch for events, rather than run in a loop
    - This could just watch for ingress events (does not need to be specific to dex)
- Add a small service to receive annotation spec via webhook 
- Generate a gRPC call to the dex service to add the client info

# Requirements

- Needs to capture when ingresses are created, modified or removed
- Needs to be quick at updating Dex config, but does not have to be instant 
- Needs to allow a fresh deployment of Dex and must converge on a config that has the latest cluster state

# Initial todo

- Confirm that gRPC is actually dynamic 

# Questions

- Does dex service endpoint need to be configured as part of the annotation (probably)
