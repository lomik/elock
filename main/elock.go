package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lomik/elock"
	"github.com/lomik/elock/etcd"
)

const APP = "elock"
const VERSION = "0.5.1"

type Config struct {
	EtcdEndpoints []string     `json:"etcd-endpoints"`
	EtcdRoot      string       `json:"etcd-root"`
	EtcdTTL       string       `json:"etcd-default-ttl"`
	EtcdRefresh   string       `json:"etcd-default-refresh"`
	EtcdTls       etcd.TlsOpts `json:"etcd-tls"`
}

func stopCommand(cmd *exec.Cmd, waitTime time.Duration, cmdStopped chan bool) {
	cmd.Process.Signal(syscall.SIGTERM)
	timer := time.AfterFunc(waitTime, func() {cmd.Process.Kill()})
	<- cmdStopped
	timer.Stop()
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
	sleepBefore := flag.Duration("sleep-before", 0, "Sleep random time (from zero to selected) before lock attempt")
	quiet := flag.Bool("quiet", false, "Don't print anything")
	waitTime := flag.Duration("wait-time", 0, "Wait time for inner command to end")

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

	if *quiet {
		log.SetOutput(ioutil.Discard)
	}

	if *sleepBefore != 0 {
		rand.Seed(time.Now().UnixNano())
		sleepTime := time.Duration(rand.Int63n((*sleepBefore).Nanoseconds()))
		if *debug {
			log.Printf("sleep-before: %s", sleepTime.String())
		}
		time.Sleep(sleepTime)
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
			EtcdTls:       config.EtcdTls,
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
			EtcdTls:       config.EtcdTls,
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
		EtcdTls:       config.EtcdTls,
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

	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	cmdStopped := make(chan bool)

	go func() {
		for s := range c {
			// sig is a ^C, handle it
			if *debug {
				log.Printf("signal received: %#v", s)
			}

			if *waitTime > 0 {
				stopCommand(cmd, *waitTime, cmdStopped)
			}
			x.Unlock()
			exit(1)
		}
	}()

	if err != nil { // lock failed
		if *runAnyway { // force run
			log.Println(err)

			err = cmd.Run()
			close(cmdStopped)

			if err != nil {
				log.Fatal(err)

			}

			return
		}

		fatal(err)
	}

	err = cmd.Start()

	if err != nil {
		x.Unlock()
		log.Fatal(err)
	}

	x.OnExpired(func() {
		if *waitTime > 0 {
			stopCommand(cmd, *waitTime, cmdStopped)
		} else {
			if *debug {
				log.Printf("lock expired, send kill to pid %d", cmd.Process.Pid)
			}
			cmd.Process.Kill()
		}
	})

	err = cmd.Wait()
	close(cmdStopped)

	x.Unlock()
	if err != nil {
		log.Fatal(err)
	}
}
