#!/bin/bash
# List all VMs

API_URL="${SANDFIRE_API:-http://localhost:9000}"

curl -s "${API_URL}/api/vms" | jq -r '
  if length == 0 then
    "No VMs found"
  else
    ["ID", "NAME", "STATE", "IP", "VCPU", "RAM"],
    (.[] | [.id, .name, .state, (.ip_address // "-"), (.vcpu_count | tostring), (.ram_mb | tostring) + "MB"])
    | @tsv
  end
' | column -t
