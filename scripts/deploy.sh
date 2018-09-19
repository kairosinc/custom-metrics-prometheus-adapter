#!/bin/bash
set -e
set -o xtrace

PROD=${1}
APP_NAME=$(echo ${GIT_URL##*/}|cut -d\. -f1)

BRANCH=${GIT_BRANCH#*/}
TAG=${BRANCH}-${GIT_COMMIT:0:7}${BUILD_NUMBER}

case ${BRANCH} in
	master)
		NAMESPACE=monitoring
		;;
	*)
		NAMESPACE=none
		;;
esac

if [[ ${NAMESPACE} = "none" ]]; then
	echo "INVALID NAMESPACE: DEPLOY SKIPPED"
	exit 0
fi

echo "Running helm lint on chart:"
helm lint ./chart/${APP_NAME}

## Call Helm to deploy
echo "Updating app: "$APP_NAME" into environment: "$NAMESPACE" version: "$TAG
helm upgrade \
	--install \
	--wait \
	--timeout 1200 \
	--namespace ${NAMESPACE} \
	--set configmap.name=env/${NAMESPACE}.yaml \
	${APP_NAME}-${NAMESPACE} \
	--set image.tag=${TAG} \
	-f ./chart/${APP_NAME}/values/${NAMESPACE}.yaml \
	./chart/${APP_NAME}

## Send message to Slack
WEBHOOK=https://hooks.slack.com/services/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
BODY='
{
	"icon_emoji" : ":so-kairos:",
	"attachments": [
		{
			"fallback": "Deployment completed here: '${BUILD_URL}'",
			"color": "#139C8A",
			"author_name": "Kairos DeployBot",
			"author_link": "'${BUILD_URL}'",
			"title": "Application Deployment Completed",
			"title_link": "'${BUILD_URL}'",
			"fields": [
				{
					"title": "App",
					"value": "'${APP_NAME}'",
					"short": true
				},
				{
					"title": "Environment",
					"value": "'${NAMESPACE}'",
					"short": true
				},
				{
					"title": "Version",
					"value": "'${TAG}'",
					"short": true
				},
				{
					"title": "Jenkins URL",
					"value": "'${BUILD_URL}'",
					"short": false
				}
			]
		}
	]
}'

ESCAPEDTEXT=$(echo ${BODY} | sed 's/"/\"/g' | sed "s/'/\'/g" )
curl -s -d "payload=${ESCAPEDTEXT}" ${WEBHOOK}

exit 0
