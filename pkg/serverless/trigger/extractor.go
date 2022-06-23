package trigger

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

// getAWSPartitionByRegion parses an AWS region and returns an AWS partition
func getAWSPartitionByRegion(region string) string {
	if strings.HasPrefix(region, "us-gov-") {
		return "aws-us-gov"
	} else if strings.HasPrefix(region, "cn-") {
		return "aws-cn"
	} else {
		return "aws"
	}
}

// ExtractAPIGatewayEventARN returns an ARN from an APIGatewayProxyRequest
func ExtractAPIGatewayEventARN(event events.APIGatewayProxyRequest, region string) string {
	requestContext := event.RequestContext
	return fmt.Sprintf("arn:%v:apigateway:%v::/restapis/%v/stages/%v", getAWSPartitionByRegion(region), region, requestContext.APIID, requestContext.Stage)
}

// ExtractAPIGatewayV2EventARN returns an ARN from an APIGatewayV2HTTPRequest
func ExtractAPIGatewayV2EventARN(event events.APIGatewayV2HTTPRequest, region string) string {
	requestContext := event.RequestContext
	return fmt.Sprintf("arn:%v:apigateway:%v::/restapis/%v/stages/%v", getAWSPartitionByRegion(region), region, requestContext.APIID, requestContext.Stage)
}

// ExtractAPIGatewayWebSocketEventARN returns an ARN from an APIGatewayWebsocketProxyRequest
func ExtractAPIGatewayWebSocketEventARN(event events.APIGatewayWebsocketProxyRequest, region string) string {
	requestContext := event.RequestContext
	return fmt.Sprintf("arn:%v:apigateway:%v::/restapis/%v/stages/%v", getAWSPartitionByRegion(region), region, requestContext.APIID, requestContext.Stage)
}

// ExtractAlbEventARN returns an ARN from an ALBTargetGroupRequest
func ExtractAlbEventARN(event events.ALBTargetGroupRequest) string {
	return event.RequestContext.ELB.TargetGroupArn
}

// ExtractCloudwatchEventARN returns an ARN from a CloudWatchEvent
func ExtractCloudwatchEventARN(event events.CloudWatchEvent) string {
	return event.Resources[0]
}

// ExtractCloudwatchLogsEventARN returns an ARN from a CloudwatchLogsEvent
func ExtractCloudwatchLogsEventARN(event events.CloudwatchLogsEvent, region string, accountID string) (string, error) {
	decodedLog, err := event.AWSLogs.Parse()
	if err != nil {
		return "", fmt.Errorf("Couldn't decode Cloudwatch Logs event: %v", err)
	}
	return fmt.Sprintf("arn:%v:logs:%v:%v:log-group:%v", getAWSPartitionByRegion(region), region, accountID, decodedLog.LogGroup), nil
}

// ExtractDynamoDBStreamEventARN returns an ARN from a DynamoDBEvent
func ExtractDynamoDBStreamEventARN(event events.DynamoDBEvent) string {
	return event.Records[0].EventSourceArn
}

// ExtractKinesisStreamEventARN returns an ARN from a KinesisEvent
func ExtractKinesisStreamEventARN(event events.KinesisEvent) string {
	return event.Records[0].EventSourceArn
}

// ExtractS3EventArn returns an ARN from a S3Event
func ExtractS3EventArn(event events.S3Event) string {
	return event.Records[0].EventSource
}

// ExtractSNSEventArn returns an ARN from a SNSEvent
func ExtractSNSEventArn(event events.SNSEvent) string {
	return event.Records[0].SNS.TopicArn
}

// ExtractSQSEventARN returns an ARN from a SQSEvent
func ExtractSQSEventARN(event events.SQSEvent) string {
	return event.Records[0].EventSourceARN
}

// GetTagsFromAPIGatewayEvent returns a tagset containing http tags from an
// APIGatewayProxyRequest
func GetTagsFromAPIGatewayEvent(event events.APIGatewayProxyRequest) map[string]string {
	httpTags := make(map[string]string)
	if event.RequestContext.DomainName != "" {
		httpTags["http.url"] = event.RequestContext.DomainName
	}
	httpTags["http.url_details.path"] = event.RequestContext.Path
	httpTags["http.method"] = event.RequestContext.HTTPMethod
	if event.Headers != nil {
		if event.Headers["Referer"] != "" {
			httpTags["http.referer"] = event.Headers["Referer"]
		}
	}
	return httpTags
}

// GetTagsFromAPIGatewayV2HTTPRequest returns a tagset containing http tags from an
// APIGatewayProxyRequest
func GetTagsFromAPIGatewayV2HTTPRequest(event events.APIGatewayV2HTTPRequest) map[string]string {
	httpTags := make(map[string]string)
	httpTags["http.url"] = event.RequestContext.DomainName
	httpTags["http.url_details.path"] = event.RequestContext.HTTP.Path
	httpTags["http.method"] = event.RequestContext.HTTP.Method
	if event.Headers != nil {
		if event.Headers["Referer"] != "" {
			httpTags["http.referer"] = event.Headers["Referer"]
		}
	}
	return httpTags
}

// GetTagsFromALBTargetGroupRequest returns a tagset containing http tags from an
// ALBTargetGroupRequest
func GetTagsFromALBTargetGroupRequest(event events.ALBTargetGroupRequest) map[string]string {
	httpTags := make(map[string]string)
	httpTags["http.url_details.path"] = event.Path
	httpTags["http.method"] = event.HTTPMethod
	if event.Headers != nil {
		if event.Headers["Referer"] != "" {
			httpTags["http.referer"] = event.Headers["Referer"]
		}
	}
	return httpTags
}

// GetTagsFromLambdaFunctionURLRequest returns a tagset containing http tags from a
// LambdaFunctionURLRequest
func GetTagsFromLambdaFunctionURLRequest(event events.LambdaFunctionURLRequest) map[string]string {
	httpTags := make(map[string]string)
	if event.RequestContext.DomainName != "" {
		httpTags["http.url"] = event.RequestContext.DomainName
	}
	httpTags["http.url_details.path"] = event.RequestContext.HTTP.Path
	httpTags["http.method"] = event.RequestContext.HTTP.Method
	if event.Headers != nil {
		if event.Headers["Referer"] != "" {
			httpTags["http.referer"] = event.Headers["Referer"]
		}
	}
	return httpTags
}

// GetStatusCodeFromHTTPResponse parses a generic payload and returns
// a status code, if it contains one. Returns an empty string if it does not.
// Ignore parsing errors silentlys
func GetStatusCodeFromHTTPResponse(rawPayload []byte) (string, error) {
	var response map[string]interface{}
	err := json.Unmarshal(rawPayload, &response)
	if err != nil {
		return "", err
	}

	// datadog-lambda-js checks if 'result' is undefined
	// so this is presumably the equivalent
	if len(rawPayload) == 0 {
		return "", nil
	}

	statusCode := response["statusCode"]
	switch statusCode.(type) {
	case float64:
		return strconv.FormatFloat(statusCode.(float64), 'f', -1, 64), nil
	case string:
		return statusCode.(string), nil
	default:
		return "", fmt.Errorf("Received unknown type for statusCode")
	}
}

// ParseArn parses an AWS ARN and returns the region and account
func ParseArn(arn string) (string, string, error) {
	arnTokens := strings.Split(arn, ":")
	if len(arnTokens) < 5 {
		return "", "", fmt.Errorf("Malformed arn %v provided", arn)
	}
	return arnTokens[3], arnTokens[4], nil
}