# 🚀 PayFlow Kubernetes Deployment Guide

**Status**: Ready to Deploy  
**Services**: 16 microservices + 5 infrastructure  
**Kubernetes Manifests**: ✅ Complete

---

## 📋 Prerequisites

Before deploying to Kubernetes, you need:

- ✅ kubectl installed and configured
- ✅ Active Kubernetes cluster (ONE of the following):
  - Docker Desktop Kubernetes (Recommended - easiest)
  - Minikube (Recommended for Windows)
  - AWS EKS / Azure AKS / GCP GKE
  - Any other Kubernetes cluster

---

## 🔧 Quick Setup Options

### **Option 1: Docker Desktop Kubernetes (RECOMMENDED)**

This is the easiest option if you already have Docker Desktop installed.

#### Step 1: Enable Kubernetes in Docker Desktop

1. Open **Docker Desktop**
2. Click **Settings** (gear icon)
3. Go to **Kubernetes** tab
4. Check **"Enable Kubernetes"**
5. Click **"Apply & Restart"**
6. Wait 5-10 minutes for cluster initialization

#### Step 2: Verify Kubernetes is Running

```powershell
kubectl cluster-info
kubectl get nodes
```

Expected output:
```
Kubernetes control plane is running at https://kubernetes.docker.internal:6443
```

#### Step 3: Deploy PayFlow Services

```powershell
cd D:\Internship\fintech-benchmarks\apps\application-1-payflow

# Deploy infrastructure (PostgreSQL, Redis, RabbitMQ, MongoDB, Jaeger)
kubectl apply -f k8s/infrastructure.yaml

# Wait for infrastructure to be ready
kubectl wait --for=condition=ready pod -l app=postgres -n payflow --timeout=300s

# Deploy all 16 microservices
kubectl apply -f k8s/services.yaml

# Wait for all services to be ready
kubectl wait --for=condition=ready pod -n payflow --timeout=300s --all
```

---

### **Option 2: Minikube (Alternative for Windows)**

If Docker Desktop Kubernetes is not available.

#### Step 1: Install Minikube

```powershell
# Download Minikube
choco install minikube   # if you have Chocolatey installed

# OR download manually from:
# https://github.com/kubernetes/minikube/releases

# Add to PATH if manual install
$env:PATH += ";$env:USERPROFILE"
```

#### Step 2: Start Minikube

```powershell
minikube start --driver=docker --memory=8192 --cpus=4
```

Wait for the cluster to start (1-2 minutes).

#### Step 3: Configure Docker to Use Minikube's Docker Daemon

```powershell
eval $(minikube docker-env)

# OR in PowerShell:
minikube docker-env | Invoke-Expression
```

#### Step 4: Build Images Inside Minikube

This step is important because Minikube has its own Docker environment.

```powershell
cd D:\Internship\fintech-benchmarks\apps\application-1-payflow

# Option A: Using make (if available)
make minikube-build

# Option B: Manually build each image
docker build -t payflow/api-gateway:latest -f cmd/api-gateway/Dockerfile .
docker build -t payflow/auth-service:latest -f cmd/auth-service/Dockerfile .
docker build -t payflow/account-service:latest -f cmd/account-service/Dockerfile .
docker build -t payflow/transaction-service:latest -f cmd/transaction-service/Dockerfile .
docker build -t payflow/ledger-service:latest -f cmd/ledger-service/Dockerfile .
docker build -t payflow/user-kyc-service:latest -f cmd/user-kyc-service/Dockerfile .
docker build -t payflow/notification-service:latest -f cmd/notification-service/Dockerfile .
docker build -t payflow/payment-connector-service:latest -f cmd/payment-connector-service/Dockerfile .
docker build -t payflow/audit-compliance-service:latest -f cmd/audit-compliance-service/Dockerfile .
docker build -t payflow/discovery-client-service:latest -f cmd/discovery-client-service/Dockerfile .
docker build -t payflow/api-gateway-service:latest -f cmd/api-gateway-service/Dockerfile .
docker build -t payflow/user-service:latest -f cmd/user-service/Dockerfile .
docker build -t payflow/bank-service:latest -f cmd/bank-service/Dockerfile .
docker build -t payflow/credit-card-service:latest -f cmd/credit-card-service/Dockerfile .
docker build -t payflow/invoice-service:latest -f cmd/invoice-service/Dockerfile .
docker build -t payflow/log-service:latest -f cmd/log-service/Dockerfile .
```

#### Step 5: Deploy to Minikube

```powershell
# Deploy infrastructure
kubectl apply -f k8s/infrastructure.yaml

# Wait for infrastructure
kubectl wait --for=condition=ready pod -l app=postgres -n payflow --timeout=300s

# Deploy services
kubectl apply -f k8s/services.yaml

# Wait for all pods
kubectl wait --for=condition=ready pod -n payflow --timeout=300s --all
```

---

## 📊 Verify Deployment

### Check All Pods are Running

```powershell
# View all pods in payflow namespace
kubectl get pods -n payflow

# Expected: All pods should show "Running" status
```

Expected output:
```
NAME                                    READY   STATUS    RESTARTS   AGE
api-gateway-6c7d8f9b4c-w2kv8           1/1     Running   0          2m
auth-service-5d9e3f2b1a-x3lm9          1/1     Running   0          2m
account-service-7f1a2b3c4d-y4np6       1/1     Running   0          2m
transaction-service-8g2b3c4d5e-z5oq7   1/1     Running   0          2m
ledger-service-9h3c4d5e6f-a6pr8        1/1     Running   0          2m
... (remaining services)
postgres-0                              1/1     Running   0          3m
redis-7c8d9e0f1a-b1st2                 1/1     Running   0          3m
rabbitmq-9d0e1f2g3h-c2tu3              1/1     Running   0          3m
mongodb-0e1f2g3h4i-d3uv4               1/1     Running   0          3m
jaeger-1f2g3h4i5j-e4vw5                1/1     Running   0          3m
```

### Check Services

```powershell
kubectl get svc -n payflow
```

### Check Service Details

```powershell
kubectl describe svc api-gateway -n payflow
```

### View Logs

```powershell
# View logs from a specific pod
kubectl logs -n payflow api-gateway-6c7d8f9b4c-w2kv8

# Follow logs in real-time
kubectl logs -n payflow api-gateway-6c7d8f9b4c-w2kv8 -f

# View logs from all pods with label app=api-gateway
kubectl logs -n payflow -l app=api-gateway -f
```

---

## 🔌 Access Services

### Via kubectl port-forward

```powershell
# Forward API Gateway (localhost:8080 -> pod:8080)
kubectl port-forward -n payflow svc/api-gateway 8080:8080

# In another terminal, access:
curl http://localhost:8080/health
```

### Via Kubernetes Service DNS

Inside cluster, services are accessible via:
```
http://service-name:port
http://api-gateway:8080
http://auth-service:8081
```

### Via LoadBalancer (if configured)

The API Gateway is configured as a LoadBalancer service. Depending on your cluster:

**Docker Desktop:**
```powershell
kubectl get svc api-gateway -n payflow
# Copy the EXTERNAL-IP and use it to access the service
```

**Minikube:**
```powershell
minikube tunnel

# In another terminal:
kubectl get svc api-gateway -n payflow
# Use the EXTERNAL-IP
```

---

## 🛠️ Common Operations

### Scale a Service

```powershell
# Scale api-gateway to 5 replicas
kubectl scale deployment api-gateway -n payflow --replicas=5

# Check replicas
kubectl get deployment -n payflow
```

### Update a Service Image

```powershell
# Update api-gateway to a new version
kubectl set image deployment/api-gateway -n payflow \
  api-gateway=payflow/api-gateway:v2.0.0
```

### Delete Services

```powershell
# Delete just the services
kubectl delete -f k8s/services.yaml -n payflow

# Delete everything including infrastructure
kubectl delete -f k8s/
```

### View Resource Usage

```powershell
# View CPU and memory usage
kubectl top pods -n payflow
kubectl top nodes

# Note: This requires metrics-server to be installed
```

---

## 🐛 Troubleshooting

### Pods remain in Pending state

```powershell
# Describe the pod to see the issue
kubectl describe pod <pod-name> -n payflow

# Common causes:
# - Insufficient resources (memory/CPU)
# - Node issues
# - PVC not provisioned
```

### Pods keep crashing (CrashLoopBackOff)

```powershell
# View logs to see the error
kubectl logs <pod-name> -n payflow

# Common causes:
# - Service can't connect to database
# - Missing environment variables
# - Health check failing
```

### ImagePullBackOff

```powershell
# Error: Images not found
# Solution: Make sure images are built in the cluster's Docker environment

# For Minikube:
minikube docker-env | Invoke-Expression
docker build -t payflow/api-gateway:latest -f cmd/api-gateway/Dockerfile .

# For Docker Desktop:
# Make sure you ran docker compose build locally
```

### Services can't communicate

```powershell
# Test DNS resolution
kubectl run -it --rm debug --image=busybox --restart=Never -n payflow -- sh
nslookup api-gateway
nslookup postgres
```

---

## 📈 Monitoring

### View Cluster Status

```powershell
# Get cluster info
kubectl cluster-info

# View nodes
kubectl get nodes
kubectl describe nodes

# View namespaces
kubectl get ns
```

### View Events

```powershell
# Recent cluster events
kubectl get events -n payflow

# Specific pod events
kubectl describe pod <pod-name> -n payflow
```

### Access Jaeger Tracing

The Jaeger service is deployed and running. Access it:

```powershell
# Port forward to Jaeger
kubectl port-forward -n payflow svc/jaeger 16686:16686

# Open browser: http://localhost:16686
```

---

## 🎯 Deployment Summary

| Component | Status | Details |
|-----------|--------|---------|
| Kubernetes Manifests | ✅ Complete | All 16 services + infrastructure |
| Docker Images | ✅ Built | Available locally and in Docker Desktop/Minikube |
| Database (PostgreSQL) | ✅ Ready | StatefulSet with persistent storage |
| Cache (Redis) | ✅ Ready | Single deployment |
| Message Broker (RabbitMQ) | ✅ Ready | With management UI |
| Document DB (MongoDB) | ✅ Ready | For audit logs |
| Tracing (Jaeger) | ✅ Ready | With OTLP support |
| API Gateway | ✅ Ready | LoadBalancer service |
| 15 Microservices | ✅ Ready | Individual deployments with services |
| ConfigMaps | ✅ Ready | Shared configuration |
| Secrets | ✅ Ready | Credentials management |

---

## 📝 Next Steps

1. **Choose your deployment option** (Docker Desktop or Minikube)
2. **Follow the setup steps** for your chosen option
3. **Verify deployment** with kubectl commands
4. **Access services** via port-forward or LoadBalancer
5. **Monitor** using kubectl logs and Jaeger

---

## 📚 Additional Resources

- [Kubernetes Documentation](https://kubernetes.io/docs/home/)
- [kubectl Cheat Sheet](https://kubernetes.io/docs/reference/kubectl/cheatsheet/)
- [Minikube Documentation](https://minikube.sigs.k8s.io/docs/)
- [Docker Desktop Kubernetes](https://docs.docker.com/desktop/features/kubernetes/)

---

**You now have everything ready to deploy PayFlow to Kubernetes!** 🚀
