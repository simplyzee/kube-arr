# kube-arr

Your *arr media stack on Kubernetes, from one Helm chart. Sonarr, Radarr, Lidarr, Bazarr, Prowlarr, Jackett, and your choice of Jellyfin, Plex or Emby. Every app is optional, they all share one media volume, and they reach each other by name.

```bash
helm repo add simplyzee https://charts.simplyzee.dev
helm install media simplyzee/kube-arr -n media --create-namespace \
  --set global.timezone=Europe/London
```

## Why kube-arr

- **One install, any mix of apps.** Switch each app on or off, and choose Plex, Jellyfin, Emby or no media server.
- **One shared media volume** mounted at the same path everywhere, so imports are instant moves or hardlinks, not copies. It can be a new PVC, an existing one, NFS, or a disk on the node.
- **Built-in cloud storage.** Set `rclone.enabled=true` and kube-arr runs [kube-rclone](https://github.com/simplyzee/kube-rclone) for you, mounting Google Drive, S3, Dropbox or any rclone remote on every node as your media library.
- **The apps find each other.** Each gets a stable in-cluster address, such as `http://media-sonarr:8989`, ready to paste into Prowlarr or Bazarr.
- **Your data is safe.** Config and media volumes are kept when you uninstall.
- **Ready to expose.** Set `ingress.domain` and each app appears at `sonarr.<domain>`, `radarr.<domain>` and so on, with TLS.
- **Sensible defaults:** pinned LinuxServer.io images, probes, Recreate updates for the SQLite databases, read-only media for the media server, and a values schema with clear errors.
- **Tested on every change:** a real install in a kind cluster checks every app responds and that they share the volume.

See the [chart README](kube-arr/README.md) for storage options, wiring the apps together, Plex claiming, hardware transcoding and every setting.

## Contributing

Issues and pull requests are welcome.

## Licence

Apache 2.0
