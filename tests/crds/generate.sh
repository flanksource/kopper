#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONTROLLER_GEN="${CONTROLLER_GEN:-${ROOT_DIR}/bin/controller-gen}"

if [[ ! -x "${CONTROLLER_GEN}" ]]; then
  echo "controller-gen not found at ${CONTROLLER_GEN}. Run 'make controller-gen' first." >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
trap 'trash "${TMP_DIR}"' EXIT

${CONTROLLER_GEN} crd paths="${ROOT_DIR}/tests/crdgen/v1/..." output:crd:artifacts:config="${TMP_DIR}"
mv "${TMP_DIR}/test.kopper.io_testresources.yaml" "${ROOT_DIR}/tests/crds/test.kopper.io_testresources_v1.yaml"

${CONTROLLER_GEN} crd paths="${ROOT_DIR}/tests/crdgen/v2/..." output:crd:artifacts:config="${TMP_DIR}"
mv "${TMP_DIR}/test.kopper.io_testresources.yaml" "${ROOT_DIR}/tests/crds/test.kopper.io_testresources_v2.yaml"
