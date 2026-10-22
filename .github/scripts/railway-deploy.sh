#!/usr/bin/env bash
set -euo pipefail

: "${RAILWAY_TOKEN:?RAILWAY_TOKEN secret is not set}"
: "${RAILWAY_SERVICE_ID:?RAILWAY_SERVICE_ID variable is not set}"
: "${RAILWAY_ENVIRONMENT_ID:?RAILWAY_ENVIRONMENT_ID variable is not set}"
: "${GITHUB_SHA:?GITHUB_SHA is not set}"

graphql() {
  local response
  response=$(curl -fsS https://backboard.railway.com/graphql/v2 \
    -H "Project-Access-Token: ${RAILWAY_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$1")
  if jq -e '.errors' <<<"${response}" >/dev/null; then
    echo "Railway API error: $(jq -c '.errors' <<<"${response}")" >&2
    return 1
  fi
  echo "${response}"
}

deploy_request=$(jq -n \
  --arg service "${RAILWAY_SERVICE_ID}" \
  --arg environment "${RAILWAY_ENVIRONMENT_ID}" \
  --arg commit "${GITHUB_SHA}" \
  '{query: "mutation($service: String!, $environment: String!, $commit: String) { serviceInstanceDeployV2(serviceId: $service, environmentId: $environment, commitSha: $commit) }",
    variables: {service: $service, environment: $environment, commit: $commit}}')
deployment=$(graphql "${deploy_request}" | jq -er '.data.serviceInstanceDeployV2')
echo "Deploying ${GITHUB_SHA} as Railway deployment ${deployment}"

status_request=$(jq -n --arg id "${deployment}" \
  '{query: "query($id: String!) { deployment(id: $id) { status } }", variables: {id: $id}}')
previous=""
for _ in $(seq 1 80); do
  status=$(graphql "${status_request}" | jq -er '.data.deployment.status')
  if [[ "${status}" != "${previous}" ]]; then
    echo "Status: ${status}"
    previous="${status}"
  fi
  case "${status}" in
    SUCCESS) exit 0 ;;
    FAILED | CRASHED | REMOVED | SKIPPED) exit 1 ;;
  esac
  sleep 15
done
echo "Deployment ${deployment} did not finish within 20 minutes" >&2
exit 1
