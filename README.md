# kube-acm-importer
Import certificates into AWS ACM from Kubernetes Secrets.

## Description

AWS Load Balancer Controller allows you to specify a certificate in ACM to sit in front of your load balancer. This is done using an annotation on the service, `service.beta.kubernetes.io/aws-load-balancer-ssl-cert`. In order to use this feature the certificate must first be uploaded to ACM, this controller will import certificates from Kubernetes secrets into ACM so they can be used on Kubernetes Services. It also supports adding the annotation to specified services automatically.

In this example a certificate signed using cert-manager is uploaded to ACM and used on a Service.

```yaml
# Nginx deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: nginx
  replicas: 1
  template:
    metadata:
      labels:
        app.kubernetes.io/name: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
---
# Service in front of nginx, this will have the annotation injected into it by the ACMCertificateImport
apiVersion: v1
kind: Service
metadata:
  name: nginx
spec:
  type: LoadBalancer
  selector:
    app.kubernetes.io/name: nginx
  ports:
    - protocol: TCP
      port: 80
      targetPort: 80
---
# Create a self signed issuer
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned
spec:
  selfSigned: {}
---
# Create a certificate using the self signed issuer
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: examplecom
spec:
  secretName: examplecom-secret
  commonName: example.com
  dnsNames:
  - example.com
  issuerRef:
    name: selfsigned
    kind: Issuer
    group: cert-manager.io
---
# Upload the certificate to ACM and set the annotation on the service
apiVersion: acm.kubespress.com/v1alpha1
kind: ACMCertificateImport
metadata:
  name: examplecom
spec:
  secretRef:
    name: examplecom-secret
  serviceRefs:
  - name: nginx
```

## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

### Running on the cluster
1. Install Instances of Custom Resources:

```sh
kubectl apply -f config/samples/
```

2. Build the image and deploy the controller to the cluster with the image specified by `IMG` and `TAG`:

```sh
make deploy IMG=<some-registry>/kube-acm-importer TAG=latest
```

### Uninstall CRDs
To delete the CRDs from the cluster:

```sh
make uninstall
```

### Undeploy controller
UnDeploy the controller from the cluster:

```sh
make undeploy
```

### How it works
This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/).

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/),
which provide a reconcile function responsible for synchronizing resources until the desired state is reached on the cluster.

### Test It Out
1. Install the CRDs into the cluster:

```sh
make install
```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

```sh
make run
```

**NOTE:** You can also run this in one step by running: `make install run`

### Modifying the API definitions
If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```sh
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2023 Adam Talbot.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

