package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"go-scada/stream"
)

const usage = `Usage:
  scada [-config path] get [-type type] SUBJECT
  scada [-config path] set [-type type] [-file path] SUBJECT [VALUE]

Types:
  string (default), bool, int64, float64, bytes,
  int64[], float64[], string[], bool[]

Arrays are JSON encoded. Bytes are base64 encoded.
Use "-file -" to read a string value from stdin.

Examples:
  scada get -type float64 tank.temperature
  scada set -type float64 tank.temperature 82.5
  scada set -type bool pump.running true
  scada set -file alert.json tank.alert_config
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "scada:", err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	global := flag.NewFlagSet("scada", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	configPath := global.String("config", stream.DefaultConfigPath, "stream configuration file")
	global.Usage = func() {}
	if err := global.Parse(args); err != nil {
		return usageError(err.Error())
	}

	remaining := global.Args()
	if len(remaining) == 0 {
		return usageError("get or set command is required")
	}

	command := remaining[0]
	commandArgs := remaining[1:]
	if command != "get" && command != "set" {
		return usageError(fmt.Sprintf("unknown command %q", command))
	}

	valueType, subject, rawValue, err := parseCommand(command, commandArgs)
	if err != nil {
		return err
	}

	client, err := stream.New(*configPath)
	if err != nil {
		return err
	}
	defer client.Close()

	if command == "get" {
		return get(client, valueType, subject, output)
	}
	return set(client, valueType, subject, rawValue)
}

func parseCommand(command string, args []string) (string, string, string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	valueType := flags.String("type", "string", "value type")
	filePath := flags.String("file", "", "read the value from a file ('-' for stdin)")
	if err := flags.Parse(args); err != nil {
		return "", "", "", usageError(err.Error())
	}

	positionals := flags.Args()
	if len(positionals) == 0 {
		return "", "", "", usageError("subject is required")
	}
	subject := positionals[0]

	if command == "get" {
		if *filePath != "" || len(positionals) != 1 {
			return "", "", "", usageError("get accepts exactly one subject")
		}
		return *valueType, subject, "", nil
	}

	if *filePath != "" {
		if *valueType != "string" {
			return "", "", "", usageError("-file can only be used with type string")
		}
		if len(positionals) != 1 {
			return "", "", "", usageError("set with -file accepts only a subject")
		}
		value, err := readValue(*filePath)
		return *valueType, subject, value, err
	}
	if len(positionals) != 2 {
		return "", "", "", usageError("set requires a subject and value")
	}
	return *valueType, subject, positionals[1], nil
}

func readValue(path string) (string, error) {
	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("read value from %q: %w", path, err)
	}
	return string(data), nil
}

func get(client *stream.Client, valueType, subject string, output io.Writer) error {
	switch valueType {
	case "string":
		value, err := stream.Get[string](client, subject)
		return printScalar(output, value, err)
	case "bool":
		value, err := stream.Get[bool](client, subject)
		return printScalar(output, value, err)
	case "int64":
		value, err := stream.Get[int64](client, subject)
		return printScalar(output, value, err)
	case "float64":
		value, err := stream.Get[float64](client, subject)
		return printScalar(output, value, err)
	case "bytes":
		value, err := stream.Get[[]byte](client, subject)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, base64.StdEncoding.EncodeToString(value))
		return err
	case "int64[]":
		value, err := stream.Get[[]int64](client, subject)
		return printJSON(output, value, err)
	case "float64[]":
		value, err := stream.Get[[]float64](client, subject)
		return printJSON(output, value, err)
	case "string[]":
		value, err := stream.Get[[]string](client, subject)
		return printJSON(output, value, err)
	case "bool[]":
		value, err := stream.Get[[]bool](client, subject)
		return printJSON(output, value, err)
	default:
		return usageError(fmt.Sprintf("unsupported type %q", valueType))
	}
}

func set(client *stream.Client, valueType, subject, rawValue string) error {
	switch valueType {
	case "string":
		return stream.Set(client, subject, rawValue)
	case "bool":
		value, err := strconv.ParseBool(rawValue)
		if err != nil {
			return fmt.Errorf("parse bool value: %w", err)
		}
		return stream.Set(client, subject, value)
	case "int64":
		value, err := strconv.ParseInt(rawValue, 10, 64)
		if err != nil {
			return fmt.Errorf("parse int64 value: %w", err)
		}
		return stream.Set(client, subject, value)
	case "float64":
		value, err := strconv.ParseFloat(rawValue, 64)
		if err != nil {
			return fmt.Errorf("parse float64 value: %w", err)
		}
		return stream.Set(client, subject, value)
	case "bytes":
		value, err := base64.StdEncoding.DecodeString(rawValue)
		if err != nil {
			return fmt.Errorf("parse base64 bytes value: %w", err)
		}
		return stream.Set(client, subject, value)
	case "int64[]":
		return setJSON[[]int64](client, subject, rawValue)
	case "float64[]":
		return setJSON[[]float64](client, subject, rawValue)
	case "string[]":
		return setJSON[[]string](client, subject, rawValue)
	case "bool[]":
		return setJSON[[]bool](client, subject, rawValue)
	default:
		return usageError(fmt.Sprintf("unsupported type %q", valueType))
	}
}

func setJSON[V stream.Value](client *stream.Client, subject, rawValue string) error {
	var value V
	if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
		return fmt.Errorf("parse JSON value: %w", err)
	}
	return stream.Set(client, subject, value)
}

func printScalar[V any](output io.Writer, value V, err error) error {
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, value)
	return err
}

func printJSON[V any](output io.Writer, value V, err error) error {
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(value)
}

func usageError(message string) error {
	return errors.New(strings.TrimSpace(message) + "\n\n" + usage)
}
