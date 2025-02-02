#!/bin/sh

# This command runs the sample-component-build pipeline to build
# the Quarkus getting-started quickstart: https://github.com/quarkusio/quarkus-quickstarts/tree/main/getting-started

DIR=`dirname "$0"`

echo
echo "👉 Running the pipeline with a sample project:"
echo

kubectl create -f $DIR/run-build-discovery-task.yaml

echo
echo "🎉 Done! You can watch logs now with the following command: tkn tr logs --last -f"
