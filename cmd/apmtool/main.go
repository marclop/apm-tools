// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/elastic/apm-tool/apmclient"
)

type Commands struct {
	cfg apmclient.Config
}

func (cmd *Commands) getClient() (*apmclient.Client, error) {
	return apmclient.New(cmd.cfg)
}

func (cmd *Commands) envCommand(c *cli.Context) error {
	client, err := cmd.getClient()
	if err != nil {
		return err
	}

	creds, err := readCachedCredentials(cmd.cfg.APMServerURL)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if creds == nil {
		var expiry time.Time
		// First check if there's an Elastic Cloud integration policy,
		// and extract a secret token from that. Otherwise, create an
		// API Key.
		var apiKey, secretToken string
		policy, err := client.GetElasticCloudAPMInput(c.Context)
		if err != nil {
			policyErr := fmt.Errorf("error getting APM cloud input: %w", err)
			if c.Bool("verbose") {
				fmt.Fprintln(os.Stderr, policyErr)
			}
			// Create an API Key.
			fmt.Fprintln(os.Stderr, "Creating agent API Key...")
			expiryDuration := c.Duration("api-key-expiration")
			if expiryDuration > 0 {
				expiry = time.Now().Add(expiryDuration)
			}
			apiKey, err = client.CreateAgentAPIKey(c.Context, expiryDuration)
			if err != nil {
				apiKeyErr := err
				return fmt.Errorf(
					"failed to obtain agent credentials: %w",
					errors.Join(apiKeyErr, policyErr),
				)
			}
		} else {
			secretToken = policy.Get("apm-server.auth.secret_token").String()
		}
		creds = &credentials{
			Expiry:      expiry,
			APIKey:      apiKey,
			SecretToken: secretToken,
		}
		if err := updateCachedCredentials(cmd.cfg.APMServerURL, creds); err != nil {
			return err
		}
	}

	fmt.Printf("export ELASTIC_APM_SERVER_URL=%q;\n", cmd.cfg.APMServerURL)
	if creds.SecretToken != "" {
		fmt.Printf("export ELASTIC_APM_SECRET_TOKEN=%q;\n", creds.SecretToken)
	} else if creds.APIKey != "" {
		fmt.Printf("export ELASTIC_APM_API_KEY=%q;\n", creds.APIKey)
	}

	fmt.Printf("export OTEL_EXPORTER_OTLP_ENDPOINT=%q;\n", cmd.cfg.APMServerURL)
	if creds.SecretToken != "" {
		fmt.Printf("export OTEL_EXPORTER_OTLP_HEADERS=%q;\n", "Authorization=Bearer "+creds.SecretToken)
	} else if creds.APIKey != "" {
		fmt.Printf("export OTEL_EXPORTER_OTLP_HEADERS=%q;\n", "Authorization=ApiKey "+creds.APIKey)
	}

	return nil
}

func (cmd *Commands) servicesCommand(c *cli.Context) error {
	client, err := cmd.getClient()
	if err != nil {
		return err
	}
	services, err := client.ServiceSummary(c.Context)
	if err != nil {
		return err
	}
	for _, service := range services {
		fmt.Println(service)
	}
	return nil
}

func main() {
	commands := &Commands{}
	cmd := &cli.Command{
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:       "verbose",
				Usage:      "print debugging messages about progress",
				Aliases:    []string{"v"},
				Persistent: true,
			},
			&cli.StringFlag{
				Name:        "url",
				Usage:       "set the Elasticsearch URL",
				Category:    "Elasticsearch",
				Value:       "",
				Sources:     cli.EnvVars("ELASTICSEARCH_URL"),
				Destination: &commands.cfg.ElasticsearchURL,
				Persistent:  true,
				Action: func(c *cli.Context, v string) error {
					return commands.cfg.InferAPMServerURL()
				},
			},
			&cli.StringFlag{
				Name:        "username",
				Usage:       "set the Elasticsearch username",
				Category:    "Elasticsearch",
				Value:       "elastic",
				Sources:     cli.EnvVars("ELASTICSEARCH_USERNAME"),
				Destination: &commands.cfg.Username,
				Persistent:  true,
			},
			&cli.StringFlag{
				Name:        "password",
				Usage:       "set the Elasticsearch password",
				Category:    "Elasticsearch",
				Sources:     cli.EnvVars("ELASTICSEARCH_PASSWORD"),
				Destination: &commands.cfg.Password,
				Persistent:  true,
			},
			&cli.StringFlag{
				Name:        "api-key",
				Usage:       "set the Elasticsearch API Key",
				Category:    "Elasticsearch",
				Sources:     cli.EnvVars("ELASTICSEARCH_API_KEY"),
				Destination: &commands.cfg.APIKey,
				Persistent:  true,
			},
			&cli.StringFlag{
				Name:        "apm-url",
				Usage:       "set the APM Server URL. Will be derived from the Elasticsearch URL for Elastic Cloud.",
				Category:    "APM",
				Value:       "",
				Sources:     cli.EnvVars("ELASTIC_APM_SERVER_URL"),
				Destination: &commands.cfg.APMServerURL,
				Persistent:  true,
			},
		},
		Commands: []*cli.Command{{
			Name:   "agent-env",
			Usage:  "print environment variables for configuring Elastic APM agents",
			Action: commands.envCommand,
			Flags: []cli.Flag{
				&cli.DurationFlag{
					Name:  "api-key-expiration",
					Usage: "specify how long before a created API Key expires. 0 means it never expires.",
				},
			},
		}, {
			Name:   "list-services",
			Usage:  "list APM services",
			Action: commands.servicesCommand,
		}},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}