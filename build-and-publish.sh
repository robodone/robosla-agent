#!/bin/bash

readonly SUFFIX=${1}

set -ue

cd ~/src/github.com/robodone/agent


if [[ -z  "$SUFFIX"  ]] ; then
    echo "Please, specify the release suffix, like '1' or 'alpha' as the sole command line arg."
    exit 1
fi

readonly RELEASE="$(date +%Y-%m-%d)-$1"
echo "Making a release ${RELEASE}"

echo "Building from source ..."
GOARCH=amd64 go get github.com/robodone/agent
GOARCH=arm GOARM=7 go get github.com/robodone/agent

echo "Patching robosla-agent.json manifest ..."
cp -f robosla-agent.json /tmp/robosla-agent.json
sed -i "s/RELEASE_NAME/${RELEASE}/" /tmp/robosla-agent.json

echo "Copying to Google Cloud Storage ..."
gsutil cp -a public-read ~/bin/agent "gs://robosla-agent/${RELEASE}/amd64/agent"
gsutil cp -a public-read ~/bin/linux_arm/agent "gs://robosla-agent/${RELEASE}/arm/agent"
gsutil cp -a public-read /tmp/robosla-agent.json gs://robosla-agent/robosla-agent.json
