/*
Copyright © 2022 CFC4N <cfc4n.cs@gmail.com>

*/
package cmd

import (
	"context"
	"ecapture/pkg/util/kernel"
	"ecapture/user/config"
	"ecapture/user/module"
	"errors"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
)

var oc = config.NewOpensslConfig()
var gc = config.NewGnutlsConfig()
var nc = config.NewNsprConfig()
var goc = config.NewGoSSLConfig()

// opensslCmd represents the openssl command
var opensslCmd = &cobra.Command{
	Use:     "tls",
	Aliases: []string{"openssl", "gnutls", "nss"},
	Short:   "use to capture tls/ssl text content without CA cert. (Support Linux 4.18/Android 5.4)",
	Long: `use eBPF uprobe/TC to capture process event data and network data,do not used libpcap.
ecapture tls
ecapture tls --hex --pid=3423
ecapture tls -l save.log --pid=3423
ecapture tls --libssl=/lib/x86_64-linux-gnu/libssl.so.1.1
ecapture tls -w save_3_0_5.pcapng --ssl_version="openssl 3.0.5" --libssl=/lib/x86_64-linux-gnu/libssl.so.3 
ecapture tls -w save_android.pcapng -i wlan0 --libssl=/apex/com.android.conscrypt/lib64/libssl.so --ssl_version="boringssl 1.1.1" --port 443
.
`,
	Run: openSSLCommandFunc,
}

func init() {
	opensslCmd.PersistentFlags().StringVar(&oc.Curlpath, "curl", "", "curl or wget file path, use to dectet openssl.so path, default:/usr/bin/curl. (Deprecated)")
	opensslCmd.PersistentFlags().StringVar(&oc.Openssl, "libssl", "", "libssl.so file path, will automatically find it from curl default.")
	opensslCmd.PersistentFlags().StringVar(&gc.Gnutls, "gnutls", "", "libgnutls.so file path, will automatically find it from curl default.")
	opensslCmd.PersistentFlags().StringVar(&gc.Curlpath, "wget", "", "wget file path, default: /usr/bin/wget. (Deprecated)")
	opensslCmd.PersistentFlags().StringVar(&nc.Firefoxpath, "firefox", "", "firefox file path, default: /usr/lib/firefox/firefox. (Deprecated)")
	opensslCmd.PersistentFlags().StringVar(&nc.Nsprpath, "nspr", "", "libnspr44.so file path, will automatically find it from curl default.")
	opensslCmd.PersistentFlags().StringVar(&oc.Pthread, "pthread", "", "libpthread.so file path, use to hook connect to capture socket FD.will automatically find it from curl. (Deprecated)")
	opensslCmd.PersistentFlags().StringVar(&goc.Path, "gobin", "", "path to binary built with Go toolchain.")
	opensslCmd.PersistentFlags().StringVarP(&oc.Write, "write", "w", "", "write the  raw packets to file as pcapng format.")
	opensslCmd.PersistentFlags().StringVarP(&oc.Ifname, "ifname", "i", "", "(TC Classifier) Interface name on which the probe will be attached.")
	opensslCmd.PersistentFlags().Uint16Var(&oc.Port, "port", 443, "port number to capture, default:443.")
	opensslCmd.PersistentFlags().StringVar(&oc.SslVersion, "ssl_version", "", "openssl/boringssl version， e.g: --ssl_version=\"openssl 1.1.1g\" or  --ssl_version=\"boringssl 1.1.1\"")

	rootCmd.AddCommand(opensslCmd)
}

// openSSLCommandFunc executes the "bash" command.
func openSSLCommandFunc(command *cobra.Command, args []string) {
	stopper := make(chan os.Signal, 1)
	signal.Notify(stopper, os.Interrupt, syscall.SIGTERM)
	ctx, cancelFun := context.WithCancel(context.TODO())

	logger := log.New(os.Stdout, "tls_", log.LstdFlags)

	// save global config
	gConf, err := getGlobalConf(command)
	if err != nil {
		logger.Fatal(err)
	}
	if gConf.loggerFile != "" {
		f, e := os.Create(gConf.loggerFile)
		if e != nil {
			logger.Fatal(e)
			return
		}
		logger.SetOutput(f)
	}
	logger.Printf("ECAPTURE :: %s Version : %s", cliName, GitVersion)
	logger.Printf("ECAPTURE :: Pid Info : %d", os.Getpid())
	var version kernel.Version
	version, err = kernel.HostVersion()
	logger.Printf("ECAPTURE :: Kernel Info : %s", version.String())

	modNames := []string{module.MODULE_NAME_OPENSSL, module.MODULE_NAME_GNUTLS, module.MODULE_NAME_NSPR, module.MODULE_NAME_GOSSL}

	var runMods uint8
	var runModules = make(map[string]module.IModule)
	var wg sync.WaitGroup

	for _, modName := range modNames {
		mod := module.GetModuleByName(modName)
		if mod == nil {
			logger.Printf("ECAPTURE :: \tcant found module: %s", modName)
			break
		}

		var conf config.IConfig
		switch mod.Name() {
		case module.MODULE_NAME_OPENSSL:
			conf = oc
		case module.MODULE_NAME_GNUTLS:
			conf = gc
		case module.MODULE_NAME_NSPR:
			conf = nc
		case module.MODULE_NAME_GOSSL:
			conf = goc
		default:
		}

		if conf == nil {
			logger.Printf("ECAPTURE :: \tcant found module %s config info.", mod.Name())
			break
		}

		conf.SetPid(gConf.Pid)
		conf.SetUid(gConf.Uid)
		conf.SetDebug(gConf.Debug)
		conf.SetHex(gConf.IsHex)
		conf.SetNoSearch(gConf.NoSearch)

		err = conf.Check()

		if err != nil {
			// ErrorGoBINNotSET is a special error, we should not print it.
			if errors.Is(err, config.ErrorGoBINNotSET) {
				logger.Printf("%s\tmodule [disabled].", mod.Name())
				continue
			}

			logger.Printf("%s\tmodule initialization failed. [skip it]. error:%+v", mod.Name(), err)
			continue
		}

		logger.Printf("%s\tmodule initialization", mod.Name())

		//初始化
		err = mod.Init(ctx, logger, conf)
		if err != nil {
			logger.Printf("%s\tmodule initialization failed, [skip it]. error:%+v", mod.Name(), err)
			continue
		}

		// 加载ebpf，挂载到hook点上，开始监听
		//go func(module user.IModule) {
		//
		//}(mod)
		err = mod.Run()
		if err != nil {
			logger.Printf("%s\tmodule run failed, [skip it]. error:%+v", mod.Name(), err)
			continue
		}
		runModules[mod.Name()] = mod
		logger.Printf("%s\tmodule started successfully.", mod.Name())
		wg.Add(1)
		runMods++
	}

	// needs runmods > 0
	if runMods > 0 {
		logger.Printf("ECAPTURE :: \tstart %d modules", runMods)
		<-stopper
	} else {
		logger.Println("ECAPTURE :: \tNo runnable modules, Exit(1)")
		os.Exit(1)
	}
	cancelFun()

	// clean up
	for _, mod := range runModules {
		err = mod.Close()
		wg.Done()
		if err != nil {
			logger.Fatalf("%s\tmodule close failed. error:%+v", mod.Name(), err)
		}
	}

	wg.Wait()
	os.Exit(0)
}
