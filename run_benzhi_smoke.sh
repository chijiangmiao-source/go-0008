#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d ./benzhi-smoke.XXXXXX)"
pid=""
export GOCACHE="$(pwd)/${tmpdir}/gocache"
export GOMODCACHE="$(pwd)/${tmpdir}/gomodcache"

cleanup() {
  if [[ -n "${pid}" ]]; then
    kill "${pid}" >/dev/null 2>&1 || true
    wait "${pid}" >/dev/null 2>&1 || true
  fi
  rm -r "${tmpdir}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

go run ./cmd/grainfumigate -data "${tmpdir}/replay" replay >/dev/null

go run ./cmd/grainfumigate -addr 127.0.0.1:18080 -data "${tmpdir}/service" serve >"${tmpdir}/server.log" 2>&1 &
pid="$!"

ready=""
for _ in $(seq 1 40); do
  if response="$(curl -fsS http://127.0.0.1:18080/healthz 2>/dev/null)"; then
    if [[ "${response}" == *'"status":"ok"'* ]]; then
      ready="yes"
      break
    fi
  fi
  sleep 0.25
done

if [[ "${ready}" != "yes" ]]; then
  echo "service did not become healthy" >&2
  if [[ -f "${tmpdir}/server.log" ]]; then
    sed -n '1,120p' "${tmpdir}/server.log" >&2
  fi
  exit 1
fi

echo "benzhi smoke ok"
