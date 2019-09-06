#!/usr/bin/env bash
set -e

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

function start_dex() {
  kubectl apply -f ${DIR}/deployment/namespace.yaml

  sleep 1
  kubectl apply -f ${DIR}/deployment/

  kubectl rollout status -n kube-auth deployment dex --timeout=180s
}

function test_dex_early() {
  # No clients should exist at the start

  clients=$(kubectl get oauth2clients.dex.coreos.com --all-namespaces -o json | jq '.items|length')

  if [[ $clients -ne 0 ]]; then
    echo "Expecting 0 clients at this stage, got $clients instead"
    exit 1
  fi
}

function test_dex() {

  kubectl apply -f ${DIR}/examples/

  clients=$(kubectl get oauth2clients.dex.coreos.com --all-namespaces -o json | jq '.items|length')

  if [[ $clients -ne 5 ]]; then
    echo "Expecting 5 clients at this stage, got $clients instead"
    exit 1
  fi

  echo "Successfully retrieved expected 5 clients from dex"

  kubectl -n default delete ingress example
  kubectl -n default delete secret secret-with-annotations-and-labels

  clients=$(kubectl get oauth2clients.dex.coreos.com --all-namespaces -o json | jq '.items|length')

  if [[ $clients -ne 3 ]]; then
    echo "Expecting 3 clients at this stage, got $clients instead"
    exit 1
  fi

  echo "Successfully retrieved expected 3 clients from dex"
}

export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"
start_dex

sleep 5
test_dex_early
test_dex
