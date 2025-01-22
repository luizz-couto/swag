package swag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"log"
	"regexp"
	"strings"

	"github.com/swaggest/go-asyncapi/spec-2.4.0"
)

type OperationAction string
type Attribute string

const (
	Send    OperationAction = "send"
	Receive OperationAction = "receive"
)

type AsyncScope struct {
	parser *Parser
	servers map[string]*spec.ServersAdditionalProperties
	channels map[string]*spec.ChannelItem
	operations map[string]*OperationWithChannel
}

type OperationWithChannel struct {
	action OperationAction
	channel string
	spec.Operation
}

const (
	asyncHeaderAttr Attribute = "@asyncapi"
	serverAttr Attribute = "@server"
	channelAttr Attribute = "@channel"
	operationAttr Attribute = "@operation"
)

// NewAsyncOperation creates a new AsyncOperation with default properties.
func NewAsyncScope(parser *Parser) *AsyncScope {
	if parser == nil {
		parser = New()
	}

	asyncOperation := &AsyncScope{
		parser:           parser,
		servers:          make(map[string]*spec.ServersAdditionalProperties),
		channels:         make(map[string]*spec.ChannelItem),
		operations:       make(map[string]*OperationWithChannel),
	}

	return asyncOperation
}

// AttributeHandler is a map of attribute to the function that handles the attribute.
var AttributeHandler = map[Attribute]func(*AsyncScope, *string, string, *ast.File) error {
	asyncHeaderAttr: func(as *AsyncScope, s1 *string, s2 string, f *ast.File) error {
		return nil
	},
	serverAttr:  (*AsyncScope).ParseServerComment,
	channelAttr: (*AsyncScope).ParseChannelComment,
	operationAttr: (*AsyncScope).ParseOperationComment,
}

// ParseAsyncAPIComment parses the comment line and sets the AsyncAPI properties.
func (asyncScope *AsyncScope) ParseAsyncAPIComment(funcName *string, comment string, astFile *ast.File) error {
	commentLine := strings.TrimSpace(strings.TrimLeft(comment, "/"))
	if len(commentLine) == 0 {
		return nil
	}

	fields := FieldsByAnySpace(commentLine, 2)
	attribute := fields[0]
	lowerAttribute := strings.ToLower(attribute)
	
	var lineRemainder string
	if len(fields) > 1 {
		lineRemainder = fields[1]
	}

	handler, exists := AttributeHandler[Attribute(lowerAttribute)]
	if exists {
		return handler(asyncScope, funcName, lineRemainder, astFile)
	}
	
	return fmt.Errorf("unknown attribute '%s' in comment '%s'", attribute, comment)
}

var serverCommentPattern = regexp.MustCompile(`(\S+)\s+(\S+)\s+(\S+)`)

// @server {name} {protocol} {host}
func (asyncScope *AsyncScope) ParseServerComment(funcName *string, commentLine string, astFile *ast.File) error {
	matches := serverCommentPattern.FindStringSubmatch(commentLine)
	if len(matches) < 4 {
		return fmt.Errorf("missing required param comment parameters \"%s\"", commentLine)
	}

	serverName := matches[1]
	protocol := matches[2]
	host := matches[3]

	asyncScope.servers[serverName] = &spec.ServersAdditionalProperties{
		Server: &spec.Server{
			URL: host,
			Protocol: protocol,
		},
	}

	return nil
}

var channelCommentPattern = regexp.MustCompile(`(\S+)\s+(\S+)\s+"([^"]+)"`)

// @channel {name/topic} {server} "{description}"
func (asyncScope *AsyncScope) ParseChannelComment(funcName *string, commentLine string, astFile *ast.File) error {
	matches := channelCommentPattern.FindStringSubmatch(commentLine)
	log.Println(len(matches))
	if len(matches) < 4 {
		return fmt.Errorf("missing required param comment parameters \"%s\"", commentLine)
	}
	
	channelName := matches[1]
	server := matches[2]
	description := matches[3]

	asyncScope.channels[channelName] = &spec.ChannelItem{
		Servers: []string{server},
		Description: description,
	}

	return nil
}

var operationCommentPattern = regexp.MustCompile(`(\S+)\s+(\S+)\s+(\S+)\s*(.*)?`)

// @operation {id} {type} {channel} {message}
// @operation {type} {channel} {message}
func (asyncScope *AsyncScope) ParseOperationComment(funcName *string, commentLine string, astFile *ast.File) error {
	matches := operationCommentPattern.FindStringSubmatch(commentLine)
	if len(matches) < 5 {
		return fmt.Errorf("missing required param comment parameters \"%s\"", commentLine)
	}

	operationID := ""
	argsStartIndex := 1
	if matches[4] == "" {
		if funcName == nil {
			return fmt.Errorf("unable to get operation ID for commentLine '%s'", commentLine)
		}
		operationID = *funcName
	} else {
		operationID = matches[1]
		argsStartIndex = 2
	}

	operationKind := OperationAction(matches[argsStartIndex])
	if operationKind != Send && operationKind != Receive {
		return fmt.Errorf("invalid operation action '%s' for commentLine '%s'. Valid values are 'send' or 'receive' ", operationKind, commentLine)
	}

	channel := matches[argsStartIndex + 1]
	message := matches[argsStartIndex + 2]

	typeSchema, err := asyncScope.parser.getTypeSchema(message, astFile, false, true)
	if err != nil {
		log.Printf("ERROR in type schema: %v", err)
		return err
	}

	msg := spec.Message{}
	if (typeSchema.Type[0] == OBJECT) {
		jsonMarshal, err := typeSchema.Properties.MarshalJSON()
		if err != nil {
			log.Printf("ERROR in Marshal: %v", err)
			return err
		}
	
		jsonMarshal, _ = replaceStringInJSON(jsonMarshal, "#/definitions/", "#/components/schemas/")
	
		mapOfProperties := map[string]interface{}{}
		err = json.Unmarshal(jsonMarshal, &mapOfProperties)
		if err != nil {
			log.Printf("ERROR in Unmarshal: %v", err)
			return err
		}
	
		msg.OneOf1Ens().WithMessageEntity(spec.MessageEntity{
			MessageID: message,
			Payload: map[string]interface{}{
				"properties": mapOfProperties,
				"type": typeSchema.Type[0],
			},
		})
	} else {
		msg.OneOf1Ens().WithMessageEntity(spec.MessageEntity{
			MessageID: message,
			Payload: map[string]interface{}{
				"type": typeSchema.Type[0],
			},
		})
	}

	operation := spec.Operation{}
	operation.WithID(operationID).WithMessage(msg)

	asyncScope.operations[operationID] = &OperationWithChannel{
		action: operationKind,
		channel: channel,
		Operation: operation,
	}

	return nil
}

func replaceStringInJSON(originalJSON []byte, oldValue, newValue string) ([]byte, error) {
	// Replace all occurrences of oldValue with newValue
	updatedJSON := bytes.ReplaceAll(originalJSON, []byte(oldValue), []byte(newValue))
	return updatedJSON, nil
}
