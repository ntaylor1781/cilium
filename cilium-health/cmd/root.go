// Copyright 2017 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cilium/cilium/common"
	"github.com/cilium/cilium/pkg/apisocket"
	clientPkg "github.com/cilium/cilium/pkg/health/client"
	"github.com/cilium/cilium/pkg/health/defaults"
	serverPkg "github.com/cilium/cilium/pkg/health/server"
	"github.com/cilium/cilium/pkg/logfields"

	gops "github.com/google/gops/agent"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const targetName = "cilium-health"

var (
	cfgFile string
	client  *clientPkg.Client
	server  *serverPkg.Server
	log     = common.DefaultLogger
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   targetName,
	Short: "Cilium Health Agent",
	Long:  `Agent for hosting and querying the Cilium health status API`,
	Run:   run,
}

// Fatalf prints the Printf formatted message to stderr and exits the program
// Note: os.Exit(1) is not recoverable
func Fatalf(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", fmt.Sprintf(msg, args...))
	os.Exit(-1)
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	flags := rootCmd.PersistentFlags()
	flags.StringP("admin", "", string(serverPkg.AdminOptionAny), "Expose resources over 'unix' socket, 'any' socket")
	flags.BoolP("debug", "D", false, "Enable debug messages")
	flags.BoolP("daemon", "d", false, "Run as a daemon")
	flags.BoolP("passive", "p", false, "Only respond to HTTP health checks")
	flags.StringP("host", "H", "", "URI to cilum-health server API")
	flags.StringP("cilium", "c", "", "URI to Cilium server API")
	flags.IntP("interval", "i", 60, "Interval (in seconds) for periodic connectivity probes")
	flags.BoolP("json", "j", false, "Format as JSON")
	// TODO GH #2083 Hide until all commands support JSON output
	flags.MarkHidden("json")
	viper.BindPFlags(flags)
}

func getAdminOption() serverPkg.AdminOption {
	userOpt := strings.ToLower(viper.GetString("admin"))
	for _, opt := range serverPkg.AdminOptions {
		if opt == serverPkg.AdminOption(userOpt) {
			return opt
		}
	}

	Fatalf("Invalid admin option \"%s\" (must be one of %s)",
		strings.ToLower(viper.GetString("admin")), serverPkg.AdminOptions)
	return serverPkg.AdminOption("")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	viper.SetEnvPrefix("cilium-health")
	viper.SetConfigName(".cilium-health") // name of config file (without extension)
	viper.AddConfigPath("$HOME")          // adding home directory as first search path
	viper.AutomaticEnv()                  // read in environment variables that match

	if viper.GetBool("debug") {
		log.Level = logrus.DebugLevel
	} else {
		log.Level = logrus.InfoLevel
	}

	if viper.GetBool("daemon") {
		config := serverPkg.Config{
			CiliumURI:     viper.GetString("cilium"),
			Debug:         viper.GetBool("debug"),
			Passive:       viper.GetBool("passive"),
			Admin:         getAdminOption(),
			ProbeInterval: time.Duration(viper.GetInt("interval")) * time.Second,
			ProbeDeadline: time.Second,
		}
		if srv, err := serverPkg.NewServer(config); err != nil {
			Fatalf("Error while creating server: %s\n", err)
		} else {
			server = srv
		}
	} else if cl, err := clientPkg.NewClient(viper.GetString("host")); err != nil {
		Fatalf("Error while creating client: %s\n", err)
	} else {
		client = cl
	}
}

func runServer() {
	common.RequireRootPrivilege(targetName)
	os.Remove(defaults.SockPath)

	// Open socket for using gops to get stacktraces of the daemon.
	if err := gops.Listen(gops.Options{}); err != nil {
		errorString := fmt.Sprintf("unable to start gops: %s", err)
		fmt.Println(errorString)
		os.Exit(-1)
	}

	// When the unix socket is made available, set its permissions.
	go func() {
		scopedLog := log.WithField(logfields.Path, defaults.SockPath)
		for {
			_, err := os.Stat(defaults.SockPath)
			if err == nil {
				break
			}
			scopedLog.WithError(err).Debugf("Cannot find socket")
			time.Sleep(1 * time.Second)
		}
		if err := apisocket.SetDefaultPermissions(defaults.SockPath); err != nil {
			scopedLog.WithError(err).Fatal("Cannot set default permissions on socket")
		} else {
			scopedLog.Info("Successfully set default permissions on socket")
		}
	}()

	defer server.Shutdown()
	if err := server.Serve(); err != nil {
		log.WithError(err).Fatal("Failed to serve cilium-health API")
	}
}

func run(cmd *cobra.Command, args []string) {
	if viper.GetBool("daemon") {
		runServer()
	} else {
		cmd.Help()
	}
}
