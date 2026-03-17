# ─── PayFlow Makefile ─────────────────────────────────────────────────────────
# Shortcuts for Docker Compose (local) and Minikube+Istio (k8s) workflows.
#
# Usage:
#   make up          — build and start all services locally (Docker Compose)
#   make logs        — stream all service logs
#   make k8s-deploy  — deploy everything to Minikube with Istio
#   make k8s-logs    — stream inter-service (Envoy proxy) logs from a service
#   make clean       — tear down everything

APP     := application-1-payflow
NS      := payflow
GATEWAY_SVC := payflow-api-gateway

# ─── Docker Compose (local development) ──────────────────────────────────────

.PHONY: up
up:
	docker compose up --build -d

.PHONY: down
down:
	docker compose down -v

.PHONY: logs
logs:
	docker compose logs -f

.PHONY: logs-svc
## make logs-svc SVC=transaction-service
logs-svc:
	docker compose logs -f $(SVC)

# ─── Minikube — build images inside Minikube's Docker daemon ─────────────────

.PHONY: minikube-build
minikube-build:
	@echo ">> Pointing shell at Minikube's Docker daemon..."
	eval $$(minikube docker-env) && \
	docker build -t payflow/api-gateway:latest         -f cmd/api-gateway/Dockerfile         . && \
	docker build -t payflow/auth-service:latest        -f cmd/auth-service/Dockerfile        . && \
	docker build -t payflow/account-service:latest     -f cmd/account-service/Dockerfile     . && \
	docker build -t payflow/transaction-service:latest -f cmd/transaction-service/Dockerfile . && \
	docker build -t payflow/ledger-service:latest      -f cmd/ledger-service/Dockerfile      .
	@echo ">> Images built inside Minikube ✓"

# ─── Kubernetes + Istio deployment ──────────────────────────────────────────

.PHONY: k8s-deploy
k8s-deploy: minikube-build
	kubectl apply -f k8s/namespace.yaml
	kubectl apply -f k8s/config.yaml
	kubectl apply -f k8s/infra.yaml
	kubectl apply -f k8s/deployments.yaml
	kubectl apply -f istio/peer-authentication.yaml
	kubectl apply -f istio/destination-rule.yaml
	kubectl apply -f istio/gateway.yaml
	kubectl apply -f istio/virtual-service.yaml
	kubectl apply -f istio/telemetry.yaml
	kubectl apply -f observability/prometheus.yaml
	@echo ""
	@echo ">> Waiting for pods to become ready..."
	kubectl wait --for=condition=ready pod -l app=$(GATEWAY_SVC) -n $(NS) --timeout=120s
	@echo ">> PayFlow is running in Minikube ✓"
	@echo ""
	@echo ">> Access the gateway:"
	@echo "   Run 'minikube tunnel' in another terminal, then:"
	@echo "   curl http://\$$(kubectl get svc istio-ingressgateway -n istio-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}')/health"

.PHONY: k8s-status
k8s-status:
	kubectl get pods -n $(NS) -o wide
	@echo ""
	kubectl get svc -n $(NS)

# ─── Inter-service log collection ────────────────────────────────────────────

.PHONY: k8s-logs
## Stream Envoy sidecar (inter-service) logs from api-gateway pod
k8s-logs:
	kubectl logs -n $(NS) -l app=$(GATEWAY_SVC) -c istio-proxy -f --tail=50

.PHONY: k8s-logs-svc
## make k8s-logs-svc SVC=payflow-transaction-service
k8s-logs-svc:
	kubectl logs -n $(NS) -l app=$(SVC) -c istio-proxy -f --tail=50

.PHONY: k8s-logs-app
## Stream application logs (not Envoy): make k8s-logs-app SVC=payflow-auth-service
k8s-logs-app:
	kubectl logs -n $(NS) -l app=$(SVC) -c $(subst payflow-,,$(SVC)) -f --tail=50

# ─── Observability dashboards ─────────────────────────────────────────────────

.PHONY: dashboard-kiali
dashboard-kiali:
	istioctl dashboard kiali

.PHONY: dashboard-prometheus
dashboard-prometheus:
	istioctl dashboard prometheus

.PHONY: dashboard-grafana
dashboard-grafana:
	istioctl dashboard grafana

# ─── Cleanup ──────────────────────────────────────────────────────────────────

.PHONY: k8s-clean
k8s-clean:
	kubectl delete namespace $(NS) --ignore-not-found

.PHONY: clean
clean: down k8s-clean
	@echo ">> Cleaned up local and k8s resources"
