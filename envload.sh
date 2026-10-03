#!/bin/sh
# envload.sh — печатает shell-команды экспорта переменных из .env.
#
# Файл не является валидным shell-скриптом (значения с regex, скобками, $),
# поэтому «source» не годится: парсим построчно и экранируем значения в
# одинарные кавычки (апостроф внутри значения — через '\'''"'"'\''', как обычно).
#
# Использование:
#   eval "$(./envload.sh)"          # из POSIX-sh / recipe make
#   eval "$(./envload.sh .env.dev)" # другой файл
set -e
FILE="${1:-.env}"
[ -f "$FILE" ] || { echo "envload: файл $FILE не найден" >&2; exit 1; }

sed -E '
  /^(#|;|[[:space:]]*$)/d
  /^([A-Za-z_][A-Za-z0-9_]*)=/!d
  s/^([A-Za-z_][A-Za-z0-9_]*)=(.*)$/\1\t\2/
' "$FILE" | while IFS="$(printf '\t')" read -r key value; do
  escaped=$(printf '%s' "$value" | sed "s/'/'\\\\''/g")
  printf "export %s='%s'\n" "$key" "$escaped"
done
