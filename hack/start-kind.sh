#!/usr/bin/env bash
set -e

command -v kind >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "You need to download KIND from https://github.com/kubernetes-sigs/kind/releases" 
  exit 1
fi
version=$(kind version)

if [[ $version =~ ^v0.(4).[0-9]$ ]]; then
  echo "Starting up cluster"
else
  echo "Supported version are:" 
  echo " - 0.4.0"
  exit 1
fi

K8S_VERSION="${K8S_VERSION:-v1.13.7@sha256:f3f1cfc2318d1eb88d91253a9c5fa45f6e9121b6b1e65aea6c7ef59f1549aaaf}"
K8S_WORKERS="${K8S_WORKERS:-1}"

configfile=$(mktemp)

function start_kind() {
    cat > $configfile <<EOF
kind: Cluster
apiVersion: kind.sigs.k8s.io/v1alpha3
networking:
  apiServerAddress: 0.0.0.0
  # Disable default CNI and install flannel to get around DIND issues
  disableDefaultCNI: true
nodes:
- role: control-plane
  image: kindest/node:${K8S_VERSION}
EOF

for i in `seq 1 ${K8S_WORKERS}`;
do
    cat >> $configfile <<EOF
- role: worker
  image: kindest/node:${K8S_VERSION}
EOF
done

    kind create cluster --config $configfile

    export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"
    
    # install flannel
    kubectl apply -f https://raw.githubusercontent.com/coreos/flannel/v0.11.0/Documentation/kube-flannel.yml
}

function load_image() {                                                                 
  IMAGE=$1                                  
  kind load docker-image "$IMAGE"
}  

function install_ingress() {
    export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"
    kubectl apply -f https://raw.githubusercontent.com/containous/traefik/v1.7/examples/k8s/traefik-rbac.yaml >&2
    kubectl apply -f https://raw.githubusercontent.com/containous/traefik/v1.7/examples/k8s/traefik-deployment.yaml >&2
    kubectl rollout status -n kube-system deployment traefik-ingress-controller --timeout=180s >&2

    IP=$(kubectl get pod -n kube-system -l k8s-app=traefik-ingress-lb -o json | jq '.items[0].status.hostIP' -r)
    PORT=$(kubectl get services -n kube-system traefik-ingress-service -o json | jq -r '.spec.ports[] | select(.name=="web")|.nodePort')

    echo "http://${IP}:${PORT}"
}

function install_certmanager() {

    export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"
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

function install_dex() {
  local ING_URL=$1

  kubectl create namespace kube-auth

  cat <<EOF | kubectl apply -f -
---
apiVersion: certmanager.k8s.io/v1alpha1
kind: Certificate
metadata:
  labels:
    app.kubernetes.io/component: identity-service
    app.kubernetes.io/name: dex
    app.kubernetes.io/part-of: dex
  name: dex-tls
  namespace: kube-auth
spec:
  commonName: dex
  duration: 87600h
  isCA: false
  issuerRef:
    kind: ClusterIssuer
    name: global-ss-issuer
  renewBefore: 8760h
  secretName: dex-tls
EOF

  cat <<EOF | kubectl apply -f -
---
apiVersion: v1
data:
  config.yaml: |
    issuer: http://dex.kind.com
    storage:
      type: sqlite3
      config:
        file: /tmp/dex.db

    web:
      http: 0.0.0.0:5556

    grpc:
      addr: 127.0.0.1:5557

    frontend:
      theme: "coreos"
      issuer: "Mintel"
      issuerUrl: "https://dex.kind.com"
      logoUrl: https://i.pinimg.com/originals/d3/97/8a/d3978a3830404998788e8c83dfa6f476.png
    
    telemetry:
      http: 0.0.0.0:5558

    expiry:
      signingKeys: "10m"
      idTokens: "240m"

    logger:
      level: debug
      format: json

    staticClients:
    - id: "auth"
      name: "auth"
      secret: secret
      redirectURIs:
      - https://dex-auth.kind.com/callback/fc1
      - https://auth.kind.com/callback/fc1

    enablePasswordDB: true
    staticPasswords:
    - email: "admin@example.com"
      # bcrypt hash of the string "password"
      hash: "$2a$10$2b2cU8CPhOTaGrs1HRQuAueS7JTT5ZHsHSzYiFPm1leZck7Mc8T4W"
      username: "admin"
      userID: "08a8684b-db88-4b73-90a9-3cd1661f5466"
kind: ConfigMap
metadata:
  labels:
    app.kubernetes.io/component: identity-service
    app.kubernetes.io/name: dex
    app.kubernetes.io/part-of: dex
    name: dex
  name: dex
  namespace: kube-auth
EOF

  cat <<EOF | kubectl apply -f -
---
apiVersion: v1
kind: ServiceAccount
metadata:
  labels:
    app.kubernetes.io/component: identity-service
    app.kubernetes.io/name: dex
    app.kubernetes.io/part-of: dex
    name: dex
  name: dex
  namespace: kube-auth
EOF

  cat <<EOF | kubectl apply -f -
---
apiVersion: v1
kind: Service
metadata:
  labels:
    app.kubernetes.io/component: identity-service
    app.kubernetes.io/name: dex
    app.kubernetes.io/part-of: dex
    name: dex
  name: dex
  namespace: kube-auth
spec:
  ports:
  - name: http
    port: 5556
    protocol: TCP
    targetPort: http
  - name: metrics
    port: 5558
    protocol: TCP
    targetPort: metrics
  selector:
    app.kubernetes.io/component: identity-service
    app.kubernetes.io/name: dex
    app.kubernetes.io/part-of: dex
    name: dex
  sessionAffinity: None
  type: ClusterIP
EOF

  cat <<EOF | kubectl apply -f -
---
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  annotations:
    kubernetes.io/ingress.class: traefik
  labels:
    app.kubernetes.io/component: identity-service
    app.kubernetes.io/name: dex
    app.kubernetes.io/part-of: dex
    name: dex
  name: dex
  namespace: kube-auth
spec:
  rules:
  - host: dex.kind.com
    http:
      paths:
      - backend:
          serviceName: dex
          servicePort: 5556
        path: /
EOF

  cat <<EOF | kubectl apply -f -
---
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    app.kubernetes.io/component: identity-service
    app.kubernetes.io/name: dex
    app.kubernetes.io/part-of: dex
    name: dex
  name: dex
  namespace: kube-auth
spec:
  minReadySeconds: 30
  progressDeadlineSeconds: 600
  replicas: 1
  revisionHistoryLimit: 10
  selector:
    matchLabels:
      app.kubernetes.io/component: identity-service
      app.kubernetes.io/name: dex
      app.kubernetes.io/part-of: dex
      name: dex
  strategy:
    rollingUpdate:
      maxSurge: 25%
      maxUnavailable: 0
    type: RollingUpdate
  template:
    metadata:
      labels:
        app.kubernetes.io/component: identity-service
        app.kubernetes.io/name: dex
        app.kubernetes.io/part-of: dex
        name: dex
    spec:
      containers:
      - command:
        - /usr/local/bin/dex
        - serve
        - /etc/dex/conf/config.yaml
        image: quay.io/dexidp/dex:v2.18.0
        imagePullPolicy: IfNotPresent
        livenessProbe:
          initialDelaySeconds: 5
          tcpSocket:
            port: 5556
          timeoutSeconds: 3
        name: dex
        ports:
        - containerPort: 5556
          name: http
          protocol: TCP
        - containerPort: 5558
          name: metrics
        readinessProbe:
          failureThreshold: 3
          httpGet:
            path: /healthz
            port: 5556
            scheme: HTTP
          initialDelaySeconds: 5
          periodSeconds: 10
        volumeMounts:
        - mountPath: /etc/dex/conf
          name: config
      - command:
        - /app/bin/dex-k8s-ingress-watcher
        - serve
        - --incluster
        - --dex-grpc-address
        - 127.0.0.1:5557
        image: mintel/dex-k8s-ingress-watcher:0.3.0
        imagePullPolicy: IfNotPresent
        livenessProbe:
          failureThreshold: 3
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
        name: dex-k8s-ingress-watcher
        readinessProbe:
          failureThreshold: 3
          httpGet:
            path: /readiness
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
      serviceAccount: dex
      serviceAccountName: dex
      terminationGracePeriodSeconds: 30
      volumes:
      - configMap:
          defaultMode: 420
          items:
          - key: config.yaml
            path: config.yaml
          name: dex-kcb727h4t6
        name: config
EOF

}

export KIND_K8S_VERSION="${K8S_VERSION}"
start_kind

#load_image banzaicloud/vault-operator:watch-external-secrets-using-labels

export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"

#kubectl rollout status -n kube-system daemonset kindnet --timeout=180s
kubectl rollout status -n kube-system daemonset kube-proxy --timeout=180s
kubectl rollout status -n kube-system deployment coredns --timeout=180s

echo "Installing Cert Manager"
install_certmanager

echo "Installing Ingress"
ING_URL=$(install_ingress)

echo "Installing Dex"
install_dex $ING_URL

echo ""
echo "Kind Cluster is ready"
echo "  === "
echo "export KUBECONFIG=\"$(kind get kubeconfig-path --name=\"kind\")\""
echo ""
echo "Ingress available at $ING_URL"
