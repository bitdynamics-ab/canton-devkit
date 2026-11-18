#!/usr/bin/env bash
# shellcheck shell=bash

AI_TOOL_IDENTITY_PATTERN='(^|[^[:alnum:]])(claude|anthropic|cursor|cursoragent)([^[:alnum:]]|$)'

ai_tool_identity_from_command() {
  local var="$1" text="$2"
  if [[ "${text}" =~ (^|[[:space:]])${var}=\'([^\']*)\' ]]; then
    printf '%s' "${BASH_REMATCH[2]}"
  elif [[ "${text}" =~ (^|[[:space:]])${var}=\"([^\"]*)\" ]]; then
    printf '%s' "${BASH_REMATCH[2]}"
  elif [[ "${text}" =~ (^|[[:space:]])${var}=([^[:space:]]+) ]]; then
    printf '%s' "${BASH_REMATCH[2]}"
  fi
}
