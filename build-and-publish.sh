#!/bin/bash

readonly SUFFIX=${1}

set -ue

cd ~/src/github.com/robodone/robosla-agent


if [[ -z  "$SUFFIX"  ]] ; then
    echo "Please, specify the release suffix, like '1' or 'alpha' as the sole command line arg."
    exit 1
fi

readonly RELEASE="$(date +%Y-%m-%d)-$1"
echo "Making a release ${RELEASE}"

echo "Building from source ..."
rm -f ~/bin/robosla-agent ~/bin/linux_arm/robosla-agent
GOARCH=amd64 go get -ldflags "-X main.Version=$RELEASE" github.com/robodone/robosla-agent
GOARCH=arm GOARM=7 go get -ldflags "-X main.Version=$RELEASE" github.com/robodone/robosla-agent

echo "Patching robosla-agent.json manifest ..."
cp -f robosla-agent.json /tmp/robosla-agent.json
sed -i "s/RELEASE_NAME/${RELEASE}/" /tmp/robosla-agent.json

echo "Copying to Google Cloud Storage ..."
gsutil cp -a public-read ~/bin/robosla-agent "gs://robosla-agent/${RELEASE}/amd64/robosla-agent"
gsutil cp -a public-read ~/bin/linux_arm/robosla-agent "gs://robosla-agent/${RELEASE}/arm/robosla-agent"
gsutil cp -a public-read /tmp/robosla-agent.json gs://robosla-agent/robosla-agent.json
