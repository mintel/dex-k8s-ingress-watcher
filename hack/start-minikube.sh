#!/usr/bin/env bash
set -e

command -v minikube >/dev/null 2>/dev/null
if [ $? -ne 0 ]; then 
  echo "You need to download KIND from https://github.com/kubernetes-sigs/kind/releases" 
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

function load_image() {                                                                 
  IMAGE=$1                                  
  kind load docker-image "$IMAGE"
}  

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

function install_dex() {
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
      hash: "\$2a\$10\$2b2cU8CPhOTaGrs1HRQuAueS7JTT5ZHsHSzYiFPm1leZck7Mc8T4W"
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
    kubernetes.io/ingress.class: nginx
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
          name: dex
        name: config
EOF

}

start_minikube

echo "Installing Cert Manager"
install_certmanager

echo "Installing Ingress"
install_ingress

echo "Installing Dex"
install_dex $ING_URL
