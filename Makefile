.PHONY: help dev build test clean deploy

# Variables
BACKEND_DIR = backend
FRONTEND_DIR = frontend
DOCKER_COMPOSE = docker-compose
DOCKER_COMPOSE_PROD = docker-compose -f docker-compose.prod.yml
KUBECTL = kubectl
TERRAFORM_DIR = infrastructure/terraform
HELM_DIR = infrastructure/helm

help: ## Afficher l'aide
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

setup: ## Configuration initiale du projet
	@echo "\U0001f527 Configuration du projet StackShip..."
	@./scripts/development/setup-dev.sh

dev: ## D�marrer l'environnement de d�veloppement
	@echo "\U0001f680 D�marrage de l'environnement de d�veloppement..."
	@$(DOCKER_COMPOSE) up -d postgres redis
	@echo "\u2705 Services d�marr�s. Lancez 'make run-backend' et 'make run-frontend' dans des terminaux s�par�s."

run-backend: ## D�marrer le backend Go
	@echo "\U0001f527 D�marrage du backend..."
	@cd $(BACKEND_DIR) && go run main.go

run-frontend: ## D�marrer le frontend React
	@echo "\u269b\ufe0f D�marrage du frontend..."
	@cd $(FRONTEND_DIR) && npm run dev

build: ## Builder les applications
	@echo "\U0001f4e6 Build des applications..."
	@cd $(BACKEND_DIR) && go build -o main .
	@cd $(FRONTEND_DIR) && npm run build

test: ## Ex�cuter tous les tests
	@echo "\U0001f9ea Ex�cution des tests..."
	@./scripts/development/run-tests.sh

test-backend: ## Tests backend uniquement
	@echo "\U0001f527 Tests backend..."
	@cd $(BACKEND_DIR) && go test ./...

test-frontend: ## Tests frontend uniquement
	@echo "\u269b\ufe0f Tests frontend..."
	@cd $(FRONTEND_DIR) && npm test

test-integration: ## Tests d'int�gration
	@echo "\U0001f517 Tests d'int�gration..."
	@cd tests/integration && go test -v ./...

test-e2e: ## Tests end-to-end
	@echo "\U0001f3ad Tests end-to-end..."
	@cd tests/e2e && npx cypress run

test-load: ## Tests de charge
	@echo "\u26a1 Tests de charge..."
	@cd tests/load && k6 run k6-load-test.js

lint: ## Linter le code
	@echo "\U0001f50d Linting du code..."
	@cd $(BACKEND_DIR) && go fmt ./...
	@cd $(FRONTEND_DIR) && npm run lint
	@./scripts/development/lint.sh

clean: ## Nettoyer les artifacts
	@echo "\U0001f9f9 Nettoyage..."
	@cd $(BACKEND_DIR) && go clean
	@cd $(FRONTEND_DIR) && rm -rf dist node_modules/.cache
	@$(DOCKER_COMPOSE) down -v

docker-build: ## Builder les images Docker
	@echo "\U0001f433 Build des images Docker..."
	@$(DOCKER_COMPOSE) build

docker-up: ## D�marrer avec Docker Compose
	@echo "\U0001f433 D�marrage avec Docker Compose..."
	@$(DOCKER_COMPOSE) up -d

docker-down: ## Arr�ter Docker Compose
	@echo "\U0001f433 Arr�t de Docker Compose..."
	@$(DOCKER_COMPOSE) down

docker-logs: ## Voir les logs Docker
	@$(DOCKER_COMPOSE) logs -f

docker-prod: ## D�marrer en mode production
	@echo "\U0001f680 D�marrage en mode production..."
	@$(DOCKER_COMPOSE_PROD) up -d

deploy: ## D�ployer en production
	@echo "\U0001f680 D�ploiement..."
	@./scripts/deployment/deploy.sh

deploy-staging: ## D�ployer en staging
	@echo "\U0001f3af D�ploiement en staging..."
	@./scripts/deployment/deploy.sh staging

rollback: ## Effectuer un rollback
	@echo "\u23ea Rollback..."
	@./scripts/deployment/rollback.sh

health-check: ## V�rifier la sant� des services
	@echo "\U0001f3e5 V�rification de la sant�..."
	@./scripts/deployment/health-check.sh

monitor: ## Ouvrir les dashboards de monitoring
	@echo "\U0001f4ca Ouverture des dashboards..."
	@open http://localhost:3001 # Grafana
	@open http://localhost:9090 # Prometheus

setup-monitoring: ## Configurer le monitoring
	@echo "\U0001f4c8 Configuration du monitoring..."
	@./scripts/monitoring/setup-monitoring.sh

install-deps: ## Installer les d�pendances
	@echo "\U0001f4e6 Installation des d�pendances..."
	@cd $(BACKEND_DIR) && go mod download
	@cd $(FRONTEND_DIR) && npm install

update-deps: ## Mettre � jour les d�pendances
	@echo "\U0001f504 Mise � jour des d�pendances..."
	@cd $(BACKEND_DIR) && go get -u ./...
	@cd $(FRONTEND_DIR) && npm update

security-scan: ## Scanner les vuln�rabilit�s
	@echo "\U0001f512 Scan de s�curit�..."
	@cd $(BACKEND_DIR) && go list -json -m all | nancy sleuth
	@cd $(FRONTEND_DIR) && npm audit

# Base de donn�es
db-migrate: ## Ex�cuter les migrations
	@echo "\U0001f5c4\ufe0f Ex�cution des migrations..."
	@./scripts/database/migrate.sh

db-backup: ## Sauvegarder la base de donn�es
	@echo "\U0001f4be Sauvegarde de la base de donn�es..."
	@./scripts/database/backup.sh

db-restore: ## Restaurer la base de donn�es
	@echo "\U0001f504 Restauration de la base de donn�es..."
	@./scripts/database/restore.sh

# Kubernetes
k8s-deploy: ## D�ployer sur Kubernetes
	@echo "\u2638\ufe0f D�ploiement Kubernetes..."
	@$(KUBECTL) apply -f infrastructure/kubernetes/

k8s-delete: ## Supprimer de Kubernetes
	@echo "\U0001f5d1\ufe0f Suppression Kubernetes..."
	@$(KUBECTL) delete -f infrastructure/kubernetes/

k8s-status: ## Statut des pods Kubernetes
	@echo "\U0001f4cb Statut Kubernetes..."
	@$(KUBECTL) get pods -n stackship

k8s-logs: ## Logs des pods Kubernetes
	@echo "\U0001f4dc Logs Kubernetes..."
	@$(KUBECTL) logs -f -l app=stackship -n stackship

# Terraform
terraform-init: ## Initialiser Terraform
	@echo "\U0001f3d7\ufe0f Initialisation Terraform..."
	@cd $(TERRAFORM_DIR) && terraform init

terraform-plan: ## Planifier Terraform
	@echo "\U0001f4cb Plan Terraform..."
	@cd $(TERRAFORM_DIR) && terraform plan

terraform-apply: ## Appliquer Terraform
	@echo "\U0001f680 Application Terraform..."
	@cd $(TERRAFORM_DIR) && terraform apply

terraform-destroy: ## D�truire l'infrastructure Terraform
	@echo "\U0001f4a5 Destruction Terraform..."
	@cd $(TERRAFORM_DIR) && terraform destroy

# Helm
helm-install: ## Installer avec Helm
	@echo "\u2693 Installation Helm..."
	@helm install stackship $(HELM_DIR)/stackship

helm-upgrade: ## Mettre � jour avec Helm
	@echo "\U0001f504 Mise � jour Helm..."
	@helm upgrade stackship $(HELM_DIR)/stackship

helm-uninstall: ## D�sinstaller avec Helm
	@echo "\U0001f5d1\ufe0f D�sinstallation Helm..."
	@helm uninstall stackship

# D�veloppement
dev-reset: ## R�initialiser l'environnement de d�veloppement
	@echo "\U0001f504 R�initialisation de l'environnement..."
	@$(DOCKER_COMPOSE) down -v
	@docker system prune -f
	@make setup

generate-certs: ## G�n�rer les certificats SSL
	@echo "\U0001f510 G�n�ration des certificats..."
	@./security/certificates/generate-certs.sh

format: ## Formater le code
	@echo "\u2728 Formatage du code..."
	@cd $(BACKEND_DIR) && go fmt ./...
	@cd $(FRONTEND_DIR) && npm run format

validate: ## Valider la configuration
	@echo "\u2705 Validation de la configuration..."
	@cd $(TERRAFORM_DIR) && terraform validate
	@helm lint $(HELM_DIR)/stackship

docs: ## G�n�rer la documentation
	@echo "\U0001f4da G�n�ration de la documentation..."
	@cd $(BACKEND_DIR) && godoc -http=:6060 &
	@echo "Documentation disponible sur http://localhost:6060"

benchmark: ## Ex�cuter les benchmarks
	@echo "\U0001f3c3 Benchmarks..."
	@cd $(BACKEND_DIR) && go test -bench=. ./...

profile: ## Profiler l'application
	@echo "\U0001f52c Profiling..."
	@cd $(BACKEND_DIR) && go tool pprof http://localhost:8080/debug/pprof/profile

stop: ## Arr�ter tous les services
	@echo "\u23f9\ufe0f Arr�t des services..."
	@$(DOCKER_COMPOSE) down
	@pkill -f "go run main.go" || true
	@pkill -f "npm run dev" || true

restart: ## Red�marrer les services
	@echo "\U0001f504 Red�marrage..."
	@make stop
	@make dev

all: ## Construire et tester tout
	@echo "\U0001f504 Build et test complets..."
	@make clean
	@make install-deps
	@make build
	@make test
	@make docker-build

ci: ## Pipeline CI/CD locale
	@echo "\U0001f680 Pipeline CI/CD..."
	@make lint
	@make test
	@make security-scan
	@make build
	@make docker-build