# talm pcap

Capture the network packets from the node.

## Synopsis

The command launches packet capture on the node and streams back the packets as raw pcap file.

```
talm pcap [flags]
```

## Examples

```
Default behavior is to decode the packets with internal decoder to stdout:

    talosctl pcap -i eth0

Raw pcap file can be saved with `--output` flag:

    talosctl pcap -i eth0 --output eth0.pcap

Output can be piped to tcpdump:

    talosctl pcap -i eth0 -o - | tcpdump -vvv -r -

BPF filter can be applied, but it has to compiled to BPF instructions first using tcpdump.
Correct link type should be specified for the tcpdump: EN10MB for Ethernet links and RAW
for e.g. Wireguard tunnels:

    talosctl pcap -i eth0 --bpf-filter "$(tcpdump -dd -y EN10MB 'tcp and dst port 80')"

    talosctl pcap -i kubespan --bpf-filter "$(tcpdump -dd -y RAW 'port 50000')"

As packet capture is transmitted over the network, it is recommended to filter out the Talos API traffic,
e.g. by excluding packets with the port 50000.
   
```

## Options

```
      --bpf-filter string   bpf filter to apply, tcpdump -dd format
      --duration duration   duration of the capture
  -f, --file strings        specify config files or patches in a YAML file (can specify multiple)
  -h, --help                help for pcap
  -i, --interface string    interface name to capture packets on (default "eth0")
  -o, --output string       if not set, decode packets to stdout; if set write raw pcap data to a file, use '-' for stdout
      --promiscuous         put interface into promiscuous mode
```

## Options inherited from parent commands

```
      --cluster string       Cluster to connect to if a proxy endpoint is used.
      --context string       Context to be used in command
  -e, --endpoints strings    override default endpoints in Talos configuration
      --nodes strings        target the specified nodes
      --root string          root directory of the project (default ".")
      --skip-verify          skip TLS certificate verification (keeps client authentication)
      --strict-charts        fail if the project's vendored charts/talm/ or pinned preset baseline differs from the talm binary (run talm init --update --preset <preset> to re-sync)
      --talosconfig string   The path to the Talos configuration file. Defaults to 'TALOSCONFIG' env variable if set, otherwise '$HOME/.talos/config' and '/var/run/secrets/talos.dev/config' in order.
      --version              Print the version number of the application
```

## SEE ALSO

* [talm](index.md)	 - Manage Talos the GitOps Way!

