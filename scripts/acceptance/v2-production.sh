#!/usr/bin/env bash
set -euo pipefail

readonly CREDENTIALS="${EDC_ACCEPTANCE_CREDENTIALS:?set EDC_ACCEPTANCE_CREDENTIALS to the 0600 acceptance credentials file}"

for command in curl jq openssl uuidgen; do
  command -v "$command" >/dev/null || { echo "missing command: $command" >&2; exit 1; }
done
[[ -f "$CREDENTIALS" ]] || { echo "credentials file not found: $CREDENTIALS" >&2; exit 1; }
mode="$(stat -f '%Lp' "$CREDENTIALS" 2>/dev/null || stat -c '%a' "$CREDENTIALS")"
[[ "$mode" == "600" ]] || { echo "credentials file must have mode 600, got $mode" >&2; exit 1; }
credential_server="$(jq -er '.server' "$CREDENTIALS")"
readonly SERVER="${EDC_ACCEPTANCE_SERVER:-$credential_server}"
[[ "$SERVER" == "$credential_server" ]] || {
  echo "server override does not match the credentials origin" >&2
  exit 1
}

health="$(curl -fsS "$SERVER/healthz")"
if [[ "$(jq -r '.api // ""' <<<"$health")" != "v2" ]]; then
  echo "V2 is not deployed at $SERVER; health baseline: $health" >&2
  exit 2
fi

username="$(jq -er '.username' "$CREDENTIALS")"
password="$(jq -er '.password' "$CREDENTIALS")"
project_id="$(jq -er '.project_id' "$CREDENTIALS")"
run_tag="production-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 3)"
work_dir="$(mktemp -d /tmp/edc-v2-production-acceptance.XXXXXX)"
paused=0
token=""
plugin_token=""

cleanup() {
  if [[ "$paused" == "1" && -n "$token" ]]; then
    curl -fsS -o /dev/null -H 'Content-Type: application/json' -H "Authorization: Bearer $token" \
      -d '{"action":"resume"}' -X PATCH "$SERVER/v1/projects/$project_id/plugins/acceptance-brief" || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

json_request() {
  local bearer="$1"
  local method="$2"
  local path="$3"
  local body="$4"
  curl -fsS -X "$method" -H 'Content-Type: application/json' -H "Authorization: Bearer $bearer" -d "$body" "$SERVER$path"
}

json_post() {
  json_request "$1" POST "$2" "$3"
}

mcp_call() {
  local bearer="$1"
  local name="$2"
  local arguments="$3"
  local body
  body="$(jq -nc --arg name "$name" --argjson arguments "$arguments" \
    '{jsonrpc:"2.0",id:991,method:"tools/call",params:{name:$name,arguments:$arguments}}')"
  if [[ "$arguments" == *9007199254740993* && "$body" != *9007199254740993* ]]; then
    echo "MCP request construction rounded 9007199254740993" >&2
    return 1
  fi
  curl -fsS -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H "Authorization: Bearer $bearer" -d "$body" "$SERVER/mcp"
}

login_body="$(jq -nc --arg username "$username" --arg password "$password" '{username:$username,password:$password}')"
login="$(curl -fsS -H 'Content-Type: application/json' -d "$login_body" "$SERVER/v1/auth/login")"
token="$(jq -er '.token' <<<"$login")"

isolation_project_id="$(jq -r '.isolation_project_id // ""' "$CREDENTIALS")"
if [[ -z "$isolation_project_id" ]]; then
  isolation="$(json_post "$token" /v1/projects "$(jq -nc --arg name 'V2 Acceptance Isolation' '{name:$name}')")"
  isolation_project_id="$(jq -er '.id' <<<"$isolation")"
fi

plugin_token="$(jq -r '.plugin_token // ""' "$CREDENTIALS")"
if [[ -z "$plugin_token" ]]; then
  manifest="$(jq -nc '{manifest:{id:"acceptance-brief",version:"0.1.0",name:"Acceptance brief",skills:["skills/brief/SKILL.md"],state:[{key:"current"}],processor:{entry:{type:"agent"}},config:{purpose:"production acceptance"},permissions:{read_events:["note"],write_events:[],write_state:["current"]}}}')"
  installation="$(json_post "$token" "/v1/projects/$project_id/plugins" "$manifest")"
  plugin_token="$(jq -er '.token' <<<"$installation")"
fi

credentials_tmp="$work_dir/credentials.json"
jq --arg token "$token" --arg isolation "$isolation_project_id" --arg plugin_token "$plugin_token" \
  '.token=$token | .isolation_project_id=$isolation | .plugin_token=$plugin_token' "$CREDENTIALS" > "$credentials_tmp"
chmod 0600 "$credentials_tmp"
mv "$credentials_tmp" "$CREDENTIALS"

plugin_status="$(curl -fsS -H "Authorization: Bearer $token" "$SERVER/v1/projects/$project_id/plugins" | jq -r '.plugins[] | select(.plugin_id=="acceptance-brief") | .status')"
if [[ "$plugin_status" == "paused" ]]; then
  paused=1
  json_request "$token" PATCH "/v1/projects/$project_id/plugins/acceptance-brief" '{"action":"resume"}' >/dev/null
  paused=0
fi

event_one="$(uuidgen | tr '[:upper:]' '[:lower:]')"
event_two="$(uuidgen | tr '[:upper:]' '[:lower:]')"
missing_event="$(uuidgen | tr '[:upper:]' '[:lower:]')"
event_three="$(uuidgen | tr '[:upper:]' '[:lower:]')"

first_event="$(jq -nc --arg id "$event_one" --arg run "$run_tag" '{events:[{id:$id,type:"note",content:{kind:"text",text:"production contract first event"},metadata:{run:$run,number:9007199254740993,nested:{a:1,b:2}},source:{channel:"api"}}]}')"
[[ "$first_event" == *'"number":9007199254740993'* && "$first_event" != *'"number":9007199254740992'* ]]
first_result="$(json_post "$token" "/v1/projects/$project_id/events" "$first_event")"
jq -e '.results | length==1 and .[0].status=="created"' <<<"$first_result" >/dev/null

partial_args="$(jq -nc --arg project "$project_id" --arg one "$event_one" --arg two "$event_two" --arg missing "$missing_event" --arg run "$run_tag" '{project_id:$project,events:[{id:$one,type:"note",content:{kind:"text",text:"production contract first event"},metadata:{nested:{b:2,a:1},number:9007199254740993,run:$run},source:{channel:"api"}},{id:$two,type:"note",content:{kind:"text",text:"invalid reference"},metadata:{run:$run},source:{channel:"api"},refs:[{rel:"replies_to",id:$missing}]},{id:$missing,type:"note",content:{kind:"text",text:"production contract second event"},metadata:{run:$run,number:9007199254740992},source:{channel:"api"}}]}')"
[[ "$partial_args" == *'"number":9007199254740993'* ]]
partial_result="$(mcp_call "$token" record_events "$partial_args")"
jq -e '.result.structuredContent.results | map(.status)==["duplicate","invalid","created"]' <<<"$partial_result" >/dev/null

query_body="$(jq -nc --arg run "$run_tag" '{metadata:{run:$run,number:9007199254740993}}')"
[[ "$query_body" == *'"number":9007199254740993'* && "$query_body" != *'"number":9007199254740992'* ]]
http_query="$(json_post "$token" "/v1/projects/$project_id/events/query" "$query_body")"
jq -e --arg id "$event_one" '.events | length==1 and .[0].id==$id' <<<"$http_query" >/dev/null
grep -q '"number":9007199254740993' <<<"$http_query"

mcp_query_args="$(jq -nc --arg project "$project_id" --arg run "$run_tag" '{project_id:$project,metadata:{run:$run,number:9007199254740993}}')"
[[ "$mcp_query_args" == *'"number":9007199254740993'* && "$mcp_query_args" != *'"number":9007199254740992'* ]]
mcp_query="$(mcp_call "$token" query_events "$mcp_query_args")"
jq -e --arg id "$event_one" '.result.structuredContent.events | length==1 and .[0].id==$id' <<<"$mcp_query" >/dev/null
mcp_query_structured="$(jq -c '.result.structuredContent' <<<"$mcp_query")"
grep -q '"number":9007199254740993' <<<"$mcp_query_structured"
if grep -q '"number":9007199254740992' <<<"$mcp_query_structured"; then
  echo "MCP structuredContent rounded 9007199254740993" >&2
  exit 1
fi

latest_sequence="$(jq -er '.latest_sequence' <<<"$http_query")"
state_status="$(curl -sS -o "$work_dir/state.json" -w '%{http_code}' -H "Authorization: Bearer $token" "$SERVER/v1/projects/$project_id/state/acceptance-brief/current")"
if [[ "$state_status" == "200" ]]; then
  current_version="$(jq -er '.version' "$work_dir/state.json")"
elif [[ "$state_status" == "404" ]]; then
  current_version=0
else
  echo "unexpected state read status: $state_status" >&2
  exit 1
fi

state_args="$(jq -nc --arg project "$project_id" --arg run "$run_tag" --arg ref "$event_one" --argjson version "$current_version" --argjson sequence "$latest_sequence" '{project_id:$project,key:"acceptance-brief/current",expected_version:$version,content:{format:"text",text:("acceptance "+$run)},data:{run:$run},based_on_sequence:$sequence,refs:[$ref]}')"
state_one="$(mcp_call "$plugin_token" put_state "$state_args")"
published_version="$(jq -er '.result.structuredContent.version' <<<"$state_one")"
[[ "$published_version" -eq $((current_version + 1)) ]]

third_event="$(jq -nc --arg id "$event_three" --arg run "$run_tag" '{events:[{id:$id,type:"note",content:{kind:"text",text:"event after state"},metadata:{run:$run},source:{channel:"api"}}]}')"
json_post "$token" "/v1/projects/$project_id/events" "$third_event" >/dev/null
state_with_lag="$(curl -fsS -H "Authorization: Bearer $token" "$SERVER/v1/projects/$project_id/state/acceptance-brief/current")"
jq -e --argjson version "$published_version" '.version==$version and .lag==1' <<<"$state_with_lag" >/dev/null

stale_version=$((published_version - 1))
stale_args="$(jq -nc --arg project "$project_id" --argjson version "$stale_version" --argjson sequence "$((latest_sequence + 1))" '{project_id:$project,key:"acceptance-brief/current",expected_version:$version,content:{format:"text",text:"stale"},based_on_sequence:$sequence}')"
stale_result="$(mcp_call "$plugin_token" put_state "$stale_args")"
jq -e '.result.isError==true' <<<"$stale_result" >/dev/null
grep -q 'state_version_mismatch' <<<"$stale_result"

final_args="$(jq -nc --arg project "$project_id" --arg run "$run_tag" --argjson version "$published_version" --argjson sequence "$((latest_sequence + 1))" '{project_id:$project,key:"acceptance-brief/current",expected_version:$version,content:{format:"text",text:("current "+$run)},data:{run:$run},based_on_sequence:$sequence}')"
state_two="$(mcp_call "$plugin_token" put_state "$final_args")"
jq -e --argjson version "$((published_version + 1))" '.result.structuredContent.version==$version and .result.structuredContent.lag==0' <<<"$state_two" >/dev/null

paused=1
json_request "$token" PATCH "/v1/projects/$project_id/plugins/acceptance-brief" '{"action":"pause"}' >/dev/null
paused_status="$(curl -sS -o "$work_dir/paused.json" -w '%{http_code}' -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -H "Authorization: Bearer $plugin_token" -d "$(jq -nc --arg project "$project_id" '{jsonrpc:"2.0",id:992,method:"tools/call",params:{name:"query_events",arguments:{project_id:$project}}}')" "$SERVER/mcp")"
[[ "$paused_status" == "200" ]]
jq -e '.result.isError==true' "$work_dir/paused.json" >/dev/null
grep -q 'plugin_paused' "$work_dir/paused.json"
json_request "$token" PATCH "/v1/projects/$project_id/plugins/acceptance-brief" '{"action":"resume"}' >/dev/null
paused=0
resumed_query="$(mcp_call "$plugin_token" query_events "$(jq -nc --arg project "$project_id" --arg run "$run_tag" '{project_id:$project,metadata:{run:$run}}')")"
jq -e '.result.structuredContent.events | length>=3' <<<"$resumed_query" >/dev/null

file_text="acceptance file $run_tag"
file_args="$(jq -nc --arg project "$project_id" --arg data "$(printf '%s' "$file_text" | base64 | tr -d '\n')" '{project_id:$project,filename:"acceptance.txt",media_type:"text/plain",data_base64:$data}')"
file_result="$(mcp_call "$token" upload_file "$file_args")"
file_id="$(jq -er '.result.structuredContent.file_id' <<<"$file_result")"
curl -fsS -H "Authorization: Bearer $token" "$SERVER/v1/projects/$project_id/files/$file_id" -o "$work_dir/file.txt"
[[ "$(<"$work_dir/file.txt")" == "$file_text" ]]
cross_status="$(curl -sS -o "$work_dir/cross-project.json" -w '%{http_code}' -H "Authorization: Bearer $token" "$SERVER/v1/projects/$isolation_project_id/files/$file_id")"
[[ "$cross_status" == "404" ]]

echo "PASS username=$username project_id=$project_id isolation_project_id=$isolation_project_id run_tag=$run_tag"
