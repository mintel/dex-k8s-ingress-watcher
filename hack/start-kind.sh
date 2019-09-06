#!/usr/bin/env bash
set -e

command -v kind >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "You need to download KIND from https://github.com/kubernetes-sigs/kind/releases" 
  exit 1
fi

kind version | egrep "^v0.5" >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "Need kind version 0.5.x" 
  exit 1
fi

K8S_VERSION="${K8S_VERSION:-v1.13.10@sha256:2f5f882a6d0527a2284d29042f3a6a07402e1699d792d0d5a9b9a48ef155fa2a}"
K8S_WORKERS="${K8S_WORKERS:-2}"

unset KUBECONFIG

function start_kind() {
    cat > /tmp/kind-config.yaml <<EOF
kind: Cluster
apiVersion: kind.sigs.k8s.io/v1alpha3
networking:
  apiServerAddress: 0.0.0.0
nodes:
- role: control-plane
  image: kindest/node:${K8S_VERSION}
EOF

for i in `seq 1 ${K8S_WORKERS}`;
do
    cat >> /tmp/kind-config.yaml <<EOF
- role: worker
  image: kindest/node:${K8S_VERSION}
EOF
done

    kind create cluster --config /tmp/kind-config.yaml
}

export KIND_K8S_VERSION="${K8S_VERSION}"
start_kind

export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"

kubectl rollout status -n kube-system daemonset kindnet --timeout=180s
kubectl rollout status -n kube-system daemonset kube-proxy --timeout=180s
kubectl rollout status -n kube-system deployment coredns --timeout=180s
