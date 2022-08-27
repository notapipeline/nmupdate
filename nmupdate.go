package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	gnm "github.com/Wifx/gonetworkmanager"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/context"
	"gopkg.in/yaml.v2"

	n "github.com/rjeczalik/notify"
)

const MAX_TRIES = 100

type conf struct {
	Prefix    string   `yaml:"tunnelPrefix"`
	Tunnels   []string `yaml:"tunnels"`
	Whitelist []string `yaml:"whitelist"`
}

type nmdevice struct {
	name      string
	whitelist []string
}

// check if two string slices are equal
func testEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for _, i := range a {
		if !contains(b, i) {
			return false
		}
	}
	return true
}

// check if string slice contains a given value
func contains(where []string, what string) bool {
	for _, i := range where {
		if i == what {
			return true
		}
	}
	return false
}

// Load the config file
func load(filename string) conf {
	f, _ := ioutil.ReadFile(filename)

	var c conf
	_ = yaml.Unmarshal(f, &c)
	return c
}

// Listen for all filesystem events for the config file
// and reload it if it changes
func configure(ctx context.Context, filename string, cnf *chan conf) {
	events := n.Remove | n.Write | n.InModify | n.InCloseWrite
	c := make(chan n.EventInfo, 1)
	if err := n.Watch(filename, c, events); err != nil {
		log.Fatal(err)
	}
	defer n.Stop(c)

	var config conf
	for {
		select {
		case <-ctx.Done():
			close(*cnf)
			return

		case ei := <-c:
			switch ei.Event() {
			case n.Write:
				fallthrough
			case n.InModify:
				fallthrough
			case n.InCloseWrite:
				config = load(filename)
				*cnf <- config

				// VIM is a special case and renames / removes the old buffer
				// and recreates a new one in place. This means we need to
				// set up a new watch on the file to ensure we track further
				// updates to it.
			case n.Remove:
				var i int = 0
				for {
					if _, err := os.Stat(filename); err == nil {
						break
					}
					if i == MAX_TRIES {
						// If we got here and the config wasn't recreted
						// create it with the last known config values
						data, _ := yaml.Marshal(&config)
						ioutil.WriteFile(filename, data, 0)
						break
					}
					i++
					time.Sleep(1 * time.Millisecond)
				}
				n.Stop(c)
				if err := n.Watch(filename, c, events); err != nil {
					log.Println(err)
				}
				defer n.Stop(c)
				config = load(filename)
				*cnf <- config
			}
		}
	}
}

func getDevices(ctx context.Context, nm gnm.NetworkManager, dc *chan []nmdevice) {
	var previous []string = []string{}
	for {
		select {
		case <-ctx.Done():
			close(*dc)
			return
		default:
		}

		devs := make([]nmdevice, 0)

		devices, err := nm.GetPropertyAllDevices()
		if err != nil {
			fmt.Println(err.Error())
		}

		var names []string = make([]string, 0)
		for _, device := range devices {
			d := nmdevice{}
			d.name, err = device.GetPropertyInterface()
			if err != nil {
				continue
			}
			names = append(names, d.name)

			ipconfig, _ := device.GetPropertyIP4Config()
			if ipconfig != nil {
				d.whitelist, _ = ipconfig.GetPropertySearches()
			}
			devs = append(devs, d)
		}

		if !testEq(names, previous) {
			previous = names
			*dc <- devs
		}
		// Becauswe this thread has no blocking elements it can go into overdrive
		// very easily. As once a second is enough for a scan of the interfaces, we
		// sleep between scans. this helps keep CPU low and prevents the application
		// from causing missed events through resource hogging.
		time.Sleep(1 * time.Second)
	}
}

// updates the network dsevice with a new whitelist
func updatecmd(device string, whitelist []string) {
	cmd := exec.Command("nmcli", "d", "mod", device, "ipv4.dns-search", strings.Join(whitelist, ","))
	msg, _ := cmd.Output()
	log.Println(strings.TrimSpace(string(msg)))
}

// Update all devices matching config.Prefix or config.Tunnels
func update(config conf, devices []nmdevice) {
	if !cmp.Equal(config, conf{}) && devices != nil {
		for _, d := range devices {
			if config.Prefix != "" && !strings.HasPrefix(d.name, config.Prefix) {
				continue
			} else if len(config.Tunnels) != 0 {
				if !contains(config.Tunnels, d.name) {
					continue
				}
			}
			updatecmd(d.name, config.Whitelist)
		}
	}
}

func main() {
	var (
		filename string
		config   conf
		cnf      = make(chan conf)
		netdevs  = make(chan []nmdevice)
		devices  []nmdevice
	)

	flag.StringVar(&filename, "config", "", "Path to config file")
	flag.Parse()
	if _, err := os.Stat(filename); err != nil || filename == "" {
		log.Fatalf("config file must be provided and must exist")
	}

	config = load(filename)

	nm, err := gnm.NewNetworkManager()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	c, cancel := context.WithCancel(context.Background())

	done := make(chan bool)

	go getDevices(c, nm, &netdevs)
	go configure(c, filename, &cnf)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)

	go func() {
		select {
		case <-sigc:
			cancel()
			done <- true
		}
	}()

	go func(ctx context.Context) {
		for {
			select {
			case config = <-cnf:
				go update(config, devices)
			case devices = <-netdevs:
				go update(config, devices)
			case <-ctx.Done():
				return
			}

		}
	}(c)
	<-done
}
