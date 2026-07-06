# 🎯 PAYFLOW QUICK REFERENCE CARD

## 📊 CURRENT STATUS

```
✅ DOCKER DEPLOYMENT:     COMPLETE (21/21 containers running)
🟡 KUBERNETES:            READY TO DEPLOY (manifests ready)
```

---

## 🚀 QUICK START (Docker)

```powershell
# Everything is already running!
docker ps                    # See all 21 containers

# View logs
docker compose logs -f       # All services
docker compose logs -f api-gateway  # Single service

# Test API Gateway
curl http://localhost:8080/health
```

---

## 🔗 ACCESS ALL SERVICES (Docker)

| Service | URL | Port |
|---------|-----|------|
| API Gateway | http://localhost:8080 | 8080 |
| Auth | http://localhost:8081 | 8081 |
| Account | http://localhost:8083 | 8083 |
| Transaction | http://localhost:8084 | 8084 |
| Ledger | http://localhost:8085 | 8085 |
| User KYC | http://localhost:8086 | 8086 |
| Notification | http://localhost:8087 | 8087 |
| Payment Connector | http://localhost:8088 | 8088 |
| Audit & Compliance | http://localhost:8089 | 8089 |
| Discovery Client | http://localhost:8090 | 8090 |
| API Gateway Service | http://localhost:8091 | 8091 |
| User Service | http://localhost:8092 | 8092 |
| Bank Service | http://localhost:8093 | 8093 |
| Credit Card | http://localhost:8094 | 8094 |
| Invoice | http://localhost:8095 | 8095 |
| Log Service | http://localhost:8096 | 8096 |
| **Jaeger (Traces)** | http://localhost:16686 | 16686 |
| **RabbitMQ Mgmt** | http://localhost:15672 | 15672 |

---

## 🗄️ DATABASE ACCESS

| Database | Host | Port | User | Pass |
|----------|------|------|------|------|
| PostgreSQL | localhost | 5432 | fintech | fintech123 |
| Redis | localhost | 6379 | — | — |
| MongoDB | localhost | 27017 | fintech | mongo123 |
| RabbitMQ | localhost | 5672 | fintech | rabbit123 |

---

## ⚡ DEPLOY TO KUBERNETES

### Option 1: Docker Desktop (Easiest)

```powershell
# 1. Enable in Docker Desktop Settings → Kubernetes
# 2. Wait 5 minutes for cluster to start
# 3. Run these commands:

cd D:\Internship\fintech-benchmarks\apps\application-1-payflow

kubectl apply -f k8s/infrastructure.yaml
kubectl wait --for=condition=ready pod -l app=postgres -n payflow --timeout=300s
kubectl apply -f k8s/services.yaml
kubectl wait --for=condition=ready pod -n payflow --timeout=300s --all

# View status
kubectl get pods -n payflow
```

### Option 2: Minikube

```powershell
# 1. Install Minikube
choco install minikube

# 2. Start cluster
minikube start --driver=docker --memory=8192 --cpus=4

# 3. Point to Minikube's Docker
minikube docker-env | Invoke-Expression

# 4. Build all images in Minikube
cd D:\Internship\fintech-benchmarks\apps\application-1-payflow
docker build -t payflow/api-gateway:latest -f cmd/api-gateway/Dockerfile .
# ... build remaining services

# 5. Deploy
kubectl apply -f k8s/infrastructure.yaml
kubectl apply -f k8s/services.yaml

# View status
kubectl get pods -n payflow
```

---

## 📋 KUBERNETES COMMANDS

```powershell
# View all pods
kubectl get pods -n payflow

# Watch pods in real-time
kubectl get pods -n payflow --watch

# View pod logs
kubectl logs -n payflow api-gateway-<pod-id>
kubectl logs -n payflow api-gateway-<pod-id> -f  # Follow

# View services
kubectl get svc -n payflow

# Port forward
kubectl port-forward -n payflow svc/api-gateway 8080:8080

# Access Jaeger in K8s
kubectl port-forward -n payflow svc/jaeger 16686:16686

# Delete everything
kubectl delete -f k8s/
```

---

## 📁 IMPORTANT FILES

```
📂 application-1-payflow/
  ├── docker-compose.yml              ← All 16 services config
  ├── FINAL_DEPLOYMENT_STATUS.md      ← This summary
  ├── DEPLOYMENT_STATUS.md            ← Full deployment info
  ├── KUBERNETES_DEPLOYMENT_GUIDE.md  ← Detailed K8s guide
  ├── k8s/
  │   ├── infrastructure.yaml         ← DB, Cache, Messaging
  │   └── services.yaml               ← All 16 microservices
  └── cmd/                            ← Service source code
```

---

## 🎯 16 SERVICES AT A GLANCE

| # | Service | Purpose | Port |
|---|---------|---------|------|
| 1 | api-gateway | Main entry point | 8080 |
| 2 | auth-service | Authentication | 8081 |
| 3 | account-service | Wallets & accounts | 8083 |
| 4 | transaction-service | Saga orchestration | 8084 |
| 5 | ledger-service | Double-entry bookkeeping | 8085 |
| 6 | user-kyc-service | KYC verification | 8086 |
| 7 | notification-service | Emails, SMS, push | 8087 |
| 8 | payment-connector-service | External payments | 8088 |
| 9 | audit-compliance-service | Compliance & audit | 8089 |
| 10 | discovery-client-service | Service discovery | 8090 |
| 11 | api-gateway-service | Secondary gateway | 8091 |
| 12 | user-service | User profiles | 8092 |
| 13 | bank-service | Banking operations | 8093 |
| 14 | credit-card-service | Card management | 8094 |
| 15 | invoice-service | Invoice generation | 8095 |
| 16 | log-service | Log collection | 8096 |

---

## ✅ CHECKLIST

- [x] All 16 microservices built
- [x] All services running in Docker
- [x] Infrastructure services running
- [x] Health checks passing
- [x] Docker Compose configured
- [x] Kubernetes manifests ready
- [x] ConfigMaps created
- [x] Secrets configured
- [x] Documentation complete
- [ ] Deploy to Kubernetes (Your turn!)

---

## 🚀 YOU ARE HERE

```
┌─────────────────────────────────────────────────────────────┐
│                   PAYFLOW DEPLOYMENT                        │
├─────────────────────────────────────────────────────────────┤
│ Phase 1: Docker              ✅ COMPLETE                   │
│ ├─ 16 Microservices          ✅ Running                    │
│ ├─ Infrastructure            ✅ Running                    │
│ └─ Containers                ✅ 21/21 Healthy             │
│                                                             │
│ Phase 2: Kubernetes          🔄 READY FOR YOU             │
│ ├─ Manifests                 ✅ Generated                  │
│ ├─ Configuration             ✅ Ready                      │
│ └─ Deployment Scripts        ✅ Ready                      │
│                                                             │
│ YOU ARE HERE → Choose Option 1 or 2 above & deploy!       │
└─────────────────────────────────────────────────────────────┘
```

---

## 📞 SUPPORT

- **Docker Issues**: Check `docker logs <container-name>`
- **Kubernetes Issues**: Check `kubectl logs -n payflow <pod-name>`
- **Database Issues**: Verify PostgreSQL running on 5432
- **Services won't connect**: Check Redis/RabbitMQ availability

---

**All 21 containers running in Docker ✅**  
**Ready to deploy to Kubernetes 🚀**  
**Documentation complete 📚**

**Choice is yours: Test in Docker or Deploy to Kubernetes?**
