package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type deployArgs struct {
	ZipName string `json:"zip_name" jsonschema:"The file name of the zip in Downloads, or the full path of the zip."`
	Replace *bool  `json:"replace,omitempty" jsonschema:"Set to true only after the user says yes to replace the live app."`
}

type statusArgs struct {
	Name string `json:"name" jsonschema:"The app name that deploy gave."`
}

func runMCPServer(cli *CLI) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "golaunchd"}, nil)
	registerMCPTools(server, cli)
	return server.Run(context.Background(), &mcp.StdioTransport{})
}

func registerMCPTools(server *mcp.Server, cli *CLI) {
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "deploy",
			Description: "Use when the user wants to put an app live on Launchd. Launchd builds the app on its servers. Do not build or run the app on this computer.",
		},
		func(ctx context.Context, req *mcp.CallToolRequest, args deployArgs) (*mcp.CallToolResult, any, error) {
			return mcpDeploy(cli, args)
		},
	)
	mcp.AddTool(server,
		&mcp.Tool{
			Name:        "status",
			Description: "Shows whether a Launchd app is building, live, or failed, with the last lines of the build log when it failed.",
		},
		func(ctx context.Context, req *mcp.CallToolRequest, args statusArgs) (*mcp.CallToolResult, any, error) {
			return mcpStatus(cli, args)
		},
	)
}

func mcpDeploy(cli *CLI, args deployArgs) (*mcp.CallToolResult, any, error) {
	out := &bytes.Buffer{}
	mc := cliForMCP(cli, out)

	replace := false
	if args.Replace != nil {
		replace = *args.Replace
	}
	name, err := mc.DeployZip(args.ZipName, replace)
	if errors.Is(err, errLoggedOut) {
		if loginErr := mc.Login(); loginErr != nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: loginErr.Error()}}, IsError: true}, nil, nil
		}
		name, err = mc.DeployZip(args.ZipName, replace)
	}
	if err != nil {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}, nil, nil
	}

	text := out.String()
	if text != "" {
		text += "\n"
	}
	text += "The build started. Call status with the app name " + name + " to see when it is live."
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

func mcpStatus(cli *CLI, args statusArgs) (*mcp.CallToolResult, any, error) {
	out := &bytes.Buffer{}
	mc := cliForMCP(cli, out)

	status, err := mc.Status(args.Name)
	if errors.Is(err, errLoggedOut) {
		if loginErr := mc.Login(); loginErr != nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: loginErr.Error()}}, IsError: true}, nil, nil
		}
		status, err = mc.Status(args.Name)
	}
	if err != nil {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}, nil, nil
	}

	text := fmt.Sprintf("App %s: %s.", args.Name, status.Status)
	if status.Status == "failed" && status.LogTail != "" {
		text += "\n" + status.LogTail
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

func cliForMCP(cli *CLI, out io.Writer) *CLI {
	return &CLI{
		API: cli.API, Git: cli.Git, Public: cli.Public,
		ConfigDir: cli.ConfigDir, ClaudeDir: cli.ClaudeDir,
		Dir: cli.Dir, Downloads: cli.Downloads,
		In: cli.In, Out: out,
		Interactive:  false,
		OpenBrowser:  cli.OpenBrowser,
		PollInterval: cli.PollInterval, DeployWait: cli.DeployWait,
	}
}
