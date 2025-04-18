# Local testing

To test this driver, set up AWS auth using the `aws` CLI.

Build an image that you want to test is compatible with AWS Lambda:

```
docker buildx build --platform linux/amd64 --provenance=false --push -t 12345.dkr.ecr.us-west-2.amazonaws.com/foo:test . 
```

Ensure there is an AWS role created that the Lambda function can act as:

```
aws iam create-role \
  --role-name lambda-ex \   
  --assume-role-policy-document '{"Version": "2012-10-17","Statement": [{ "Effect": "Allow", "Principal": {"Service": "lambda.amazonaws.com"}, "Action": "sts:AssumeRole"}]}'
```

```
IMAGETEST_LAMBDA_TEST_IMAGE_REF=12345.dkr.ecr.us-west-2.amazonaws.com/foo@sha256:07a99c... \
IMAGETEST_LAMBDA_ROLE=arn:aws:iam::12345:role/lambda-ex \
TF_ACC=1 \
  go test -tags=lambda ./internal/provider/... -count=1 -v -run=Lambda -timeout=5m
```

This will use your credentials to deploy the function, wait for it to be ready, invoke it, and ensure the request was successful. Then, upon completion, it will delete the Lambda function. This process normally takes about 10-15 seconds, if successful.

To skip teardown, set `IMAGETEST_LAMBDA_SKIP_TEARDOWN=true`.
