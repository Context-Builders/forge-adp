.PHONY: setup build build-mcp build-vscode-ext install-vscode-ext test migrate run-local clean \
        deploy-aws deploy-gcp deploy-azure destroy-aws destroy-gcp destroy-azure \
        helm-deploy-aws helm-deploy-gcp helm-deploy-azure

# Cloud deployment targets
# Required variables: COMPANY_ID, ENVIRONMENT (default: dev)
ENVIRONMENT ?= dev
FORGE_COMPANY_ID ?= local
FORGE_PROJECT_ID ?= local-project

## AWS (EKS + RDS)
deploy-aws:
	@echo "Deploying to AWS (EKS)..."
	cd deployments/terraform/aws && \
		terraform init && \
		terraform apply -var="company_id=$(COMPANY_ID)" -var="environment=$(ENVIRONMENT)"

destroy-aws:
	cd deployments/terraform/aws && \
		terraform destroy -var="company_id=$(COMPANY_ID)" -var="environment=$(ENVIRONMENT)"

helm-deploy-aws:
	helm upgrade --install forge ./deployments/helm/forge \
		-f deployments/helm/forge/values.yaml \
		-f deployments/helm/forge/values.aws.yaml \
		--namespace forge --create-namespace

## GCP (GKE + Cloud SQL)
deploy-gcp:
	@echo "Deploying to GCP (GKE)..."
	cd deployments/terraform/gcp && \
		terraform init && \
		terraform apply -var="company_id=$(COMPANY_ID)" -var="environment=$(ENVIRONMENT)" -var="gcp_project=$(GCP_PROJECT)"

destroy-gcp:
	cd deployments/terraform/gcp && \
		terraform destroy -var="company_id=$(COMPANY_ID)" -var="environment=$(ENVIRONMENT)" -var="gcp_project=$(GCP_PROJECT)"

helm-deploy-gcp:
	helm upgrade --install forge ./deployments/helm/forge \
		-f deployments/helm/forge/values.yaml \
		-f deployments/helm/forge/values.gcp.yaml \
		--namespace forge --create-namespace

## Azure (AKS + PostgreSQL Flexible Server)
deploy-azure:
	@echo "Deploying to Azure (AKS)..."
	cd deployments/terraform/azure && \
		terraform init && \
		terraform apply -var="company_id=$(COMPANY_ID)" -var="environment=$(ENVIRONMENT)"

destroy-azure:
	cd deployments/terraform/azure && \
		terraform destroy -var="company_id=$(COMPANY_ID)" -var="environment=$(ENVIRONMENT)"

helm-deploy-azure:
	helm upgrade --install forge ./deployments/helm/forge \
		-f deployments/helm/forge/values.yaml \
		-f deployments/helm/forge/values.azure.yaml \
		--namespace forge --create-namespace


setup:
	go mod download
	cd pkg/agents && poetry install
	cd tools/mcp-server && npm install
	cd tools/vscode-extension && npm install

build:
	go build ./...
	cd pkg/agents && poetry build
	$(MAKE) build-mcp
	$(MAKE) build-vscode-ext

build-mcp:
	@echo "Building Forge MCP server..."
	cd tools/mcp-server && npm run build

build-vscode-ext:
	@echo "Building Forge VS Code extension..."
	cd tools/vscode-extension && npm run build

install-vscode-ext: build-vscode-ext
	@echo "Packaging and installing VS Code extension..."
	cd tools/vscode-extension && \
		npx --yes @vscode/vsce package --no-dependencies && \
		code --install-extension forge-adp-*.vsix

migrate:
	@echo "Running DB migrations..."
	psql "$$DATABASE_URL" -f internal/db/migrations/000001_init_schema.up.sql

run-local:
	docker-compose -f docker-compose.dev.yml up -d
	FORGE_COMPANY_ID=$(FORGE_COMPANY_ID) FORGE_PROJECT_ID=$(FORGE_PROJECT_ID) go run ./cmd/orchestrator &
	go run ./cmd/registry &
	OPA_BUNDLE_PATH=./deployments/opa go run ./cmd/policy-engine &
	go run ./cmd/adapters/jira &
	go run ./cmd/adapters/github &
	go run ./cmd/adapters/slack &
	go run ./cmd/adapters/gitlab &
	@if [ -n "$$CONFLUENCE_BASE_URL" ]; then go run ./cmd/adapters/confluence & else echo "Skipping Confluence adapter (CONFLUENCE_BASE_URL not set)"; fi
	@if [ -n "$$LINEAR_API_KEY" ]; then go run ./cmd/adapters/linear & else echo "Skipping Linear adapter (LINEAR_API_KEY not set)"; fi

test:
	go test ./...
	cd pkg/agents && poetry run pytest tests/ -v

test-integration:
	cd pkg/agents && poetry run pytest tests/integration/ -v

lint:
	go vet ./...
	cd pkg/agents && poetry run ruff check .

docker-build:
	docker build -f Dockerfile.orchestrator -t forge/orchestrator:v0.1.0 .
	docker build -f Dockerfile.agents -t forge/agents:v0.1.0 .

clean:
	go clean ./...
	find . -name '__pycache__' -exec rm -rf {} + 2>/dev/null || true
	find . -name '*.pyc' -delete 2>/dev/null || true
	rm -rf tools/mcp-server/dist tools/mcp-server/node_modules
	rm -rf tools/vscode-extension/dist tools/vscode-extension/node_modules tools/vscode-extension/*.vsix
