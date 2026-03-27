#!/bin/bash
set -e

TENANT_ID=$1
TENANT_NAME=$2
ISOLATION=${3:-namespace}

if [ -z "$TENANT_ID" ] || [ -z "$TENANT_NAME" ]; then
    echo "Usage: $0 <tenant-id> <tenant-name> [isolation-level]"
    exit 1
fi

echo "🚀 Onboarding tenant: $TENANT_NAME ($TENANT_ID)"

# Create namespace
kubectl create namespace "forge-${TENANT_ID}" || true

# Apply network policies
kubectl apply -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: tenant-isolation
  namespace: forge-${TENANT_ID}
spec:
  podSelector: {}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              forge.io/tenant: ${TENANT_ID}
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              forge.io/shared: "true"
    - to:
        - namespaceSelector:
            matchLabels:
              forge.io/tenant: ${TENANT_ID}
EOF

# Create tenant database
psql "$DATABASE_URL" -c "CREATE DATABASE forge_${TENANT_ID};"
psql "$(echo "$DATABASE_URL" | sed "s|/[^/]*$|/forge_${TENANT_ID}|")" \
    -f internal/db/migrations/*.up.sql

# Generate tenant secrets
kubectl create secret generic "forge-${TENANT_ID}-credentials" \
    -n "forge-${TENANT_ID}" \
    --from-literal=DATABASE_URL="postgres://forge:${DB_PASSWORD}@${DB_HOST}:5432/forge_${TENANT_ID}"

# Deploy tenant-specific components
helm upgrade --install "forge-${TENANT_ID}" deployments/helm/forge \
    --namespace "forge-${TENANT_ID}" \
    --set global.tenantId="${TENANT_ID}" \
    --set global.tenantName="${TENANT_NAME}"

echo "✅ Tenant $TENANT_NAME onboarded successfully"
echo "   Namespace: forge-${TENANT_ID}"
echo "   Database: forge_${TENANT_ID}"
