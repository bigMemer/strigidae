#!/bin/sh
# Reports the token variables, or "unset".
printf '{"name":"notice","data":{"text":"%s|%s|%s|%s|%s"}}\n' \
  "${GH_TOKEN-unset}" "${GITHUB_TOKEN-unset}" "${GH_ENTERPRISE_TOKEN-unset}" \
  "${GITHUB_ENTERPRISE_TOKEN-unset}" "${KEPT-unset}"
