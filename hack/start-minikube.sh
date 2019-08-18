#!/usr/bin/env bash
set -e

command -v minikube >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "You need to download Minikube" 
  exit 1
fi

K8S_VERSION="${K8S_VERSION:-v1.13.7}"
MINIKUBE_DRIVER="${MINIKUBE_DRIVER:-kvm2}"
MINIKUBE_CPUS="${MINIKUBE_CPUS:-2}"
MINIKUBE_RAM="${MINIKUBE_RAM:-4096}"

unset KUBECONFIG

function start_minikube() {

  minikube start --vm-driver=${MINIKUBE_DRIVER} --wait=true --cpus 2 --memory 4096 --kubernetes-version=${K8S_VERSION}

}

start_minikube
