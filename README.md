# nmupdate

> Because NetworkManager on Ubuntu is broken-

This application is a replacement for DNS search management for VPN tunnels on
Ubuntu where NetworkManager is broken.

this application only handles DNS search on ipv4 (`ipv4.dns.search`) and is
designed to transparantly reload the whitelist on change.

It does this by listening for FileSystem events made against the config file.
It also re-scans the network interfaces once a second to ensure changes are
applied as soon as tunnels come up.

## Installation

Bunch of manual steps

- Build the binary (`go build .`)
- Copy to /usr/local/bin
- Setup a config file in `/etc/nmupdate/whitelist.yaml` (see below)
- Set up a systemd file

### whitelist.yaml

```
# takes precedence over tunnels
tunnelPrefix: tun

# Do not specify tunnels if tunnelPrefix above is specified
# tunnels []

# A list of hosts to add to dns-search
whitelist:
  - example.com
  - example.net
  - example.social
```

### Systemd sample file

```
[Unit]
Description=Automatically handle tunnel DNS entries
After=network.target

[Service]
ExecStart=/usr/local/bin/nmupdate -config /etc/nmupdate/whitelist.yaml
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartForceExitStatus=SIGPIPE
KillMode=control-group

[Install]
WantedBy=multi-user.target
```
