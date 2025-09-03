package main

import (
    "encoding/json"
    "fmt"
    "log"

    "github.com/aws/aws-sdk-go/aws"
    "github.com/aws/aws-sdk-go/aws/credentials"
    "github.com/aws/aws-sdk-go/aws/session"
    "github.com/aws/aws-sdk-go/service/route53"
)

// FailoverContext remains the same, defining the expected input from the YAML.
type FailoverContext struct {
    AccessKey    string `json:"accessKey"`
    SecretKey    string `json:"secretKey"`
    Region       string `json:"region"`
    HostedZoneID string `json:"hostedZoneID"`
    RecordName   string `json:"recordName"`
    RecordType   string `json:"recordType"` // "A", "CNAME", or "SRV"
    TTL          int64  `json:"ttl,omitempty"`
    TargetValue  string `json:"targetValue,omitempty"` // For A and CNAME records

    // SRV-specific fields:
    Priority int64  `json:"priority,omitempty"`
    Weight   int64  `json:"weight,omitempty"`
    Port     int64  `json:"port,omitempty"`
    Target   string `json:"target,omitempty"` // Note: This is different from TargetValue
}

// Execute is the function called by the FSM orchestrator.
// **CORRECTION 1: Changed signature from `error` to `(interface{}, error)`**
func Execute(raw map[string]interface{}) (interface{}, error) {
    var cfg FailoverContext
    data, err := json.Marshal(raw)
    if err != nil {
        // On failure, return nil for the output and the error itself.
        return nil, fmt.Errorf("failed to marshal input context: %w", err)
    }
    if err := json.Unmarshal(data, &cfg); err != nil {
        return nil, fmt.Errorf("failed to parse context: %w", err)
    }

    // --- Input Validation ---
    if cfg.AccessKey == "" || cfg.SecretKey == "" || cfg.Region == "" ||
        cfg.HostedZoneID == "" || cfg.RecordName == "" || cfg.RecordType == "" {
        return nil, fmt.Errorf("missing required field(s) in context (accessKey, secretKey, region, hostedZoneID, recordName, recordType)")
    }
    // Set default TTL if not provided
    if cfg.TTL == 0 {
        cfg.TTL = 60
    }

    // --- AWS Session Setup ---
    sess, err := session.NewSession(&aws.Config{
        Region:      aws.String(cfg.Region),
        Credentials: credentials.NewStaticCredentials(cfg.AccessKey, cfg.SecretKey, ""),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to create AWS session: %w", err)
    }
    svc := route53.New(sess)

    // **CORRECTION 2: Refactored logic to build a single ResourceRecordSet and make one API call.**
    var resourceRecordSet *route53.ResourceRecordSet

    log.Printf("Preparing DNS update for record %s of type %s", cfg.RecordName, cfg.RecordType)

    switch cfg.RecordType {
    case "A":
        if cfg.TargetValue == "" {
            return nil, fmt.Errorf("targetValue is required for A records")
        }
        resourceRecordSet = &route53.ResourceRecordSet{
            Name: aws.String(cfg.RecordName),
            Type: aws.String("A"),
            TTL:  aws.Int64(cfg.TTL),
            ResourceRecords: []*route53.ResourceRecord{
                {Value: aws.String(cfg.TargetValue)},
            },
        }
    case "CNAME":
        if cfg.TargetValue == "" {
            return nil, fmt.Errorf("targetValue is required for CNAME records")
        }
        resourceRecordSet = &route53.ResourceRecordSet{
            Name: aws.String(cfg.RecordName),
            Type: aws.String("CNAME"),
            TTL:  aws.Int64(cfg.TTL),
            ResourceRecords: []*route53.ResourceRecord{
                {Value: aws.String(cfg.TargetValue)},
            },
        }
    case "SRV":
        if cfg.Target == "" {
            return nil, fmt.Errorf("target is required for SRV records")
        }
        value := fmt.Sprintf("%d %d %d %s", cfg.Priority, cfg.Weight, cfg.Port, cfg.Target)
        resourceRecordSet = &route53.ResourceRecordSet{
            Name: aws.String(cfg.RecordName),
            Type: aws.String("SRV"),
            TTL:  aws.Int64(cfg.TTL),
            ResourceRecords: []*route53.ResourceRecord{
                {Value: aws.String(value)},
            },
        }
    default:
        return nil, fmt.Errorf("unsupported record type: '%s'. Only A, CNAME, SRV are allowed", cfg.RecordType)
    }

    // --- Construct the final API input ---
    input := &route53.ChangeResourceRecordSetsInput{
        HostedZoneId: aws.String(cfg.HostedZoneID),
        ChangeBatch: &route53.ChangeBatch{
            Comment: aws.String(fmt.Sprintf("FSM-triggered update for %s", cfg.RecordName)),
            Changes: []*route53.Change{
                {
                    Action:            aws.String("UPSERT"), // UPSERT creates or updates the record
                    ResourceRecordSet: resourceRecordSet,
                },
            },
        },
    }

    // --- Execute the API Call ---
    changeInfo, err := svc.ChangeResourceRecordSets(input)
    if err != nil {
        log.Printf("❌ Failed to update Route53 record: %v", err)
        return nil, fmt.Errorf("failed to update Route53 record: %w", err)
    }

    log.Printf("✅ DNS update submitted successfully for %s. Change ID: %s", cfg.RecordName, aws.StringValue(changeInfo.ChangeInfo.Id))

    // **CORRECTION 3: Return a structured output on success.**
    // This allows subsequent tasks to use this information.
    successOutput := map[string]interface{}{
        "status":     "SUCCESS",
        "changeId":   aws.StringValue(changeInfo.ChangeInfo.Id),
        "recordName": cfg.RecordName,
        "recordType": cfg.RecordType,
        "newValue":   resourceRecordSet.ResourceRecords[0].Value,
    }

    return successOutput, nil
}