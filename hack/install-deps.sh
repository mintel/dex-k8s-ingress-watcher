#!/usr/bin/env bash
set -e

command -v minikube >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "You need to download Minikube" 
  exit 1
fi

unset KUBECONFIG

function install_ingress() {
    minikube addons enable ingress
    sleep 5
    kubectl rollout status -n kube-system deployment nginx-ingress-controller --timeout=180s >&2
    kubectl expose -n kube-system deployment nginx-ingress-controller --type=LoadBalancer
}

function install_certmanager() {

    # Create a namespace to run cert-manager in
    kubectl create namespace cert-manager
    # Disable resource validation on the cert-manager namespace
    kubectl label namespace cert-manager certmanager.k8s.io/disable-validation=true

    kubectl apply -f https://github.com/jetstack/cert-manager/releases/download/v0.9.1/cert-manager.yaml

    kubectl rollout status -n cert-manager deployment cert-manager --timeout=180s
    kubectl rollout status -n cert-manager deployment cert-manager-cainjector --timeout=180s
    kubectl rollout status -n cert-manager deployment cert-manager-webhook --timeout=180s
    sleep 5

    # Create a global self-singed ca
    cat <<EOF | kubectl apply -f -
---
apiVersion: certmanager.k8s.io/v1alpha1
kind: ClusterIssuer
metadata:
  labels:
    app: global-ss-issuer
    app.kubernetes.io/name: global-ss-issuer
    app.kubernetes.io/instance: cert-manager
  name: global-ss-issuer
spec:
  selfSigned: {}
EOF
}

echo "Installing Cert Manager"
install_certmanager

echo "Installing Ingress"
install_ingress
