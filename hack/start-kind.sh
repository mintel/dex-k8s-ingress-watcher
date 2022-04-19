#!/usr/bin/env bash
set -ex

command -v kind >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "You need to download KIND from https://github.com/kubernetes-sigs/kind/releases" 
  exit 1
fi

kind version | egrep "^kind v0.12" >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "Need kind version 0.12.x" 
  exit 1
fi

K8S_VERSION="${K8S_VERSION:-v1.22.7@sha256:c195c17f2a9f6ad5bbddc9eb8bad68fa21709162aabf2b84e4a3896db05c0808}"
K8S_WORKERS="${K8S_WORKERS:-2}"

unset KUBECONFIG

function start_kind() {
    cat > /tmp/kind-config.yaml <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
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

export KUBECONFIG="$(mktemp)"
kind get kubeconfig --name="kind" > $KUBECONFIG

kubectl rollout status -n kube-system daemonset kindnet --timeout=180s
kubectl rollout status -n kube-system daemonset kube-proxy --timeout=180s
kubectl rollout status -n kube-system deployment coredns --timeout=180s
