# talm

Manage Talos the GitOps Way!

## Options

```
      --cluster string       Cluster to connect to if a proxy endpoint is used.
      --context string       Context to be used in command
  -e, --endpoints strings    override default endpoints in Talos configuration
  -h, --help                 help for talm
      --nodes strings        target the specified nodes
      --root string          root directory of the project (default ".")
      --skip-verify          skip TLS certificate verification (keeps client authentication)
      --strict-charts        fail if the project's vendored charts/talm/ or pinned preset baseline differs from the talm binary (run talm init --update --preset <preset> to re-sync)
      --talosconfig string   The path to the Talos configuration file. Defaults to 'TALOSCONFIG' env variable if set, otherwise '$HOME/.talos/config' and '/var/run/secrets/talos.dev/config' in order.
      --version              Print the version number of the application
```

## SEE ALSO

* [talm apply](apply.md)	 - Apply config to a Talos node
* [talm bootstrap](bootstrap.md)	 - Bootstrap the etcd cluster on the specified node.
* [talm cgroups](cgroups.md)	 - Retrieve cgroups usage information
* [talm completion](completion.md)	 - Generate the autocompletion script for the specified shell
* [talm conformance](conformance.md)	 - Run conformance tests
* [talm containers](containers.md)	 - List containers
* [talm copy](copy.md)	 - Copy data out from the node
* [talm crashdump](crashdump.md)	 - Dump debug information about the cluster
* [talm dashboard](dashboard.md)	 - Cluster dashboard with node overview, logs and real-time metrics
* [talm debug](debug.md)	 - Run a debug container from an image archive or reference
* [talm disks](disks.md)	 - Get the list of disks from /sys/block on the machine
* [talm edit](edit.md)	 - Edit Talos node machine configuration with the default editor.
* [talm etcd](etcd.md)	 - Manage etcd
* [talm events](events.md)	 - Stream runtime events
* [talm get](get.md)	 - Get a specific resource or list of resources (use 'talosctl get rd' to see all available resource types).
* [talm health](health.md)	 - Check cluster health
* [talm image](image.md)	 - Manage container images
* [talm init](init.md)	 - Initialize a new project and generate default values
* [talm inspect](inspect.md)	 - Inspect internals of Talos
* [talm interfaces](interfaces.md)	 - List network interfaces
* [talm kubeconfig](kubeconfig.md)	 - Download the admin kubeconfig from the node
* [talm list](list.md)	 - Retrieve a directory listing
* [talm logs](logs.md)	 - Retrieve logs for a service
* [talm memory](memory.md)	 - Show memory usage
* [talm meta](meta.md)	 - Write and delete keys in the META partition
* [talm mounts](mounts.md)	 - List mounts
* [talm netstat](netstat.md)	 - Show network connections and sockets
* [talm pcap](pcap.md)	 - Capture the network packets from the node.
* [talm processes](processes.md)	 - List running processes
* [talm read](read.md)	 - Read a file on the machine
* [talm reboot](reboot.md)	 - Reboot a node
* [talm reset](reset.md)	 - Reset a node
* [talm restart](restart.md)	 - Restart a process
* [talm rollback](rollback.md)	 - Rollback a node to the previous installation
* [talm rotate-ca](rotate-ca.md)	 - Rotate cluster CAs (Talos and Kubernetes APIs).
* [talm routes](routes.md)	 - List network routes
* [talm service](service.md)	 - Retrieve the state of a service (or all services), control service state
* [talm shutdown](shutdown.md)	 - Shutdown a node
* [talm stats](stats.md)	 - Get container stats
* [talm support](support.md)	 - Dump debug information about the cluster
* [talm talosconfig](talosconfig.md)	 - Regenerate talosconfig with new client certificates
* [talm template](template.md)	 - Render templates locally and display the output
* [talm time](time.md)	 - Gets current server time
* [talm upgrade](upgrade.md)	 - Upgrade Talos on the target node
* [talm usage](usage.md)	 - Retrieve a disk usage
* [talm version](version.md)	 - Prints the version
* [talm wipe](wipe.md)	 - Wipe block device or volumes

