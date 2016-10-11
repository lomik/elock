package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lomik/elock"
)

const APP = "elock"
const VERSION = "0.2"

type Config struct {
	EtcdEndpoints []string `json:"etcd-endpoints"`
	EtcdRoot      string   `json:"etcd-root"`
	EtcdTTL       string   `json:"etcd-default-ttl"`
	EtcdRefresh   string   `json:"etcd-deafult-refresh"`
}

func main() {
	configFile := flag.String("config", fmt.Sprintf("/etc/%s/%s.json", APP, APP), "Config file in json format")
	printConfig := flag.Bool("config-print-default", false, "Print default config")
	nowait := flag.Bool("n", false, "Fail rather than wait")
	slots := flag.Int("s", 1, "Available slots count for lock")
	timeout := flag.Duration("w", 0, "Wait for a limited amount of time")
	version := flag.Bool("V", false, "Display version")
	debug := flag.Bool("debug", false, "Enable debug log")
	ttl := flag.Duration("ttl", 0, "Lock ttl (default from config)")
	refresh := flag.Duration("refresh", 0, "Refresh interval (default from config)")
	minLockTime := flag.Duration("min", 0, "Minimum lock time")
	list := flag.Bool("list", false, "List all active locks")
	remove := flag.Bool("rm", false, "Remove lock by path (from list)")
	zeroExit := flag.Bool("0", false, "Exit code 0 on etcd errors or lock timeout")
	runAnyway := flag.Bool("run-anyway", false, "Run the command even if the lock could not take")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `%s %s
Usage: %s [options] etcd_key command

`, APP, VERSION, APP)
		flag.PrintDefaults()
	}

	flag.Parse()

	if *version {
		fmt.Println(APP, VERSION)
		return
	}

	config := &Config{
		EtcdEndpoints: []string{"http://localhost:2379"},
		EtcdRoot:      "/elock",
		EtcdTTL:       "1m",
		EtcdRefresh:   "10s",
	}

	if *printConfig {
		b, _ := json.MarshalIndent(config, "", "\t")
		fmt.Println(string(b))
		return
	}

	exit := func(code int) {
		if *zeroExit {
			os.Exit(0)
		} else {
			os.Exit(code)
		}
	}

	fatal := func(err error) {
		if *zeroExit {
			log.Println(err)
			os.Exit(0)
		} else {
			log.Fatal(err)
		}
	}

	cfgData, err := ioutil.ReadFile(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	err = json.Unmarshal(cfgData, config)
	if err != nil {
		log.Fatal(err)
	}

	lockTLL, err := time.ParseDuration(config.EtcdTTL)
	if err != nil {
		log.Fatal(err)
	}

	lockRefresh, err := time.ParseDuration(config.EtcdRefresh)
	if err != nil {
		log.Fatal(err)
	}

	if *ttl != 0 {
		lockTLL = *ttl
	}

	if *refresh != 0 {
		lockRefresh = *refresh
	}

	args := flag.Args()

	if *list {
		records, err := elock.List(elock.Options{
			EtcdEndpoints: config.EtcdEndpoints,
			Path:          config.EtcdRoot,
			Debug:         *debug,
		}, *timeout)

		if err != nil {
			fatal(err)
		}

		for _, r := range records {
			if r.ValueError != nil {
				fmt.Printf("[error] %s: %s\n", r.Path, r.ValueError.Error())
			} else {
				if r.IsDead() {
					d := time.Now().Sub(r.LastRefresh())
					d = time.Duration(int64(d.Seconds())) * time.Second
					fmt.Printf(
						" [died] %s: last refresh %s ago\n",
						r.Path,
						d.String(),
					)
				} else {
					fmt.Printf("   [ok] %s\n", r.Path)
				}
			}
		}

		return
	}

	if *remove {
		err := elock.Remove(elock.Options{
			EtcdEndpoints: config.EtcdEndpoints,
			Path:          config.EtcdRoot,
			Debug:         *debug,
		}, *timeout, args)

		if err != nil {
			fatal(err)
		}

		return
	}

	if len(args) < 2 || *slots < 1 {
		flag.Usage()
		return
	}

	x, err := elock.New(elock.Options{
		EtcdEndpoints: config.EtcdEndpoints,
		Path:          filepath.Join(config.EtcdRoot, args[0]),
		Slots:         *slots,
		TTL:           lockTLL,
		Refresh:       lockRefresh,
		Debug:         *debug,
		MinLockTime:   *minLockTime,
	})

	if err != nil {
		log.Fatal(err)
	}

	if *nowait {
		err = x.LockNoWait()
	} else if *timeout != 0 {
		err = x.LockTimeout(*timeout)
	} else {
		err = x.Lock()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGSTOP, syscall.SIGTERM)

	go func() {
		for _ = range c {
			// sig is a ^C, handle it
			x.Unlock()
			exit(1)
		}
	}()

	if err != nil { // lock failed
		if *runAnyway { // force run
			log.Println(err)

			cmd := exec.Command(args[1], args[2:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			err = cmd.Run()

			if err != nil {
				log.Fatal(err)

			}

			return
		}

		fatal(err)
	}

	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	err = cmd.Run()

	x.Unlock()

	if err != nil {
		log.Fatal(err)
	}
}
