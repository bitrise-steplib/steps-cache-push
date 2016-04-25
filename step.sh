#!/bin/bash

THIS_SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

set -e

export GO15VENDOREXPERIMENT="1"
export GOPATH="${THIS_SCRIPT_DIR}/go"

if [ ! -z "$workdir" ] ; then
  echo '$' cd "$workdir"
  cd "$workdir"
fi

echo
go run "${THIS_SCRIPT_DIR}/go/src/github.com/bitrise-steplib/steps-cache-push/main.go"
exit $?
