#!/bin/bash

AUTO_CONFIRM=false

while getopts ":y" opt; do
  case ${opt} in
    y ) # process option y
      AUTO_CONFIRM=true
      ;;
    \? )
      echo "Invalid Option: -$OPTARG" 1>&2
      exit 1
      ;;
  esac
done
shift $((OPTIND -1))

if [ "$AUTO_CONFIRM" = false ] ; then
    echo "Are you sure you want to proceed? (Y/n)"
    read -r confirm
    if [[ $confirm != [yY] && $confirm != [yY][eE][sS] ]]
    then
        echo "Operation cancelled."
        exit
    fi
fi

YAML_FILE="agent.codefly.yaml"

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: YAML file $YAML_FILE does not exist."
    exit 1
fi

CURRENT_VERSION=$(yq eval '.version' "$YAML_FILE")

git push -f
git push origin "v$CURRENT_VERSION" -f
