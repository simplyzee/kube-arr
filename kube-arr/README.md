# kube-arr

Run your *arr media stack on Kubernetes from one chart. Turn on the apps you want, point them at one shared media volume, and they find each other by name.

| App | What it does | Default |
|---|---|---|
| [Sonarr](https://sonarr.tv) | TV shows | on |
| [Radarr](https://radarr.video) | Movies | on |
| [Lidarr](https://lidarr.audio) | Music | off |
| [Bazarr](https://www.bazarr.media) | Subtitles | on |
| [Prowlarr](https://prowlarr.com) | Indexer manager, syncs indexers to the others | on |
| [Jackett](https://github.com/Jackett/Jackett) | Alternative indexer proxy | off |
| [Jellyfin](https://jellyfin.org), [Plex](https://www.plex.tv) or [Emby](https://emby.media) | Media server (pick one with `mediaServer`) | Jellyfin |

All apps use the [LinuxServer.io](https://www.linuxserver.io) images, pinned to tested versions.

## Install

```bash
helm repo add simplyzee https://charts.simplyzee.dev
helm repo update

helm install media simplyzee/kube-arr \
  --namespace media --create-namespace \
  --set global.timezone=Europe/London
```

This creates a 500Gi media volume and a small config volume for each app. Open Sonarr with:

```bash
kubectl port-forward -n media svc/media-sonarr 8989
```

## Choosing apps

Each app has `enabled: true/false`, and the media server is a single choice:

```yaml
lidarr:
  enabled: true      # add music
jackett:
  enabled: false
mediaServer: plex    # plex, jellyfin, emby or none
```

## The shared media volume

Every app mounts the same volume at `/data` (`media.mountPath`). Keep downloads and libraries inside it, so Sonarr and Radarr can import with an instant move or hardlink instead of copying:

```
/data
├── downloads/
└── media/
    ├── tv/
    ├── movies/
    └── music/
```

Pick one source:

| Source | Values | When to use |
|---|---|---|
| New PVC (default) | `media.persistence.size`, `.storageClass`, `.accessModes` | Single-node clusters |
| Existing PVC | `media.existingClaim: my-media` | You already have a volume |
| NFS | `media.nfs.server`, `media.nfs.path` | A NAS; works across nodes |
| Host path | `media.hostPath.path: /mnt/disks/gdrive` | A disk on the node, or a [kube-rclone](https://github.com/simplyzee/kube-rclone) mount |

`global.colocate` (on by default) keeps every app on the same node, which a ReadWriteOnce volume needs. Turn it off with NFS or a ReadWriteMany volume to spread the apps across nodes.

Media servers mount the volume read-only. Prowlarr and Jackett don't mount it at all.

## Connecting the apps

Each app gets a Service named `<release>-<app>`. With the release name `media`:

| App | Address inside the cluster |
|---|---|
| Sonarr | `http://media-sonarr:8989` |
| Radarr | `http://media-radarr:7878` |
| Lidarr | `http://media-lidarr:8686` |
| Bazarr | `http://media-bazarr:6767` |
| Prowlarr | `http://media-prowlarr:9696` |
| Jackett | `http://media-jackett:9117` |
| Jellyfin / Emby | `http://media-jellyfin:8096` / `http://media-emby:8096` |
| Plex | `http://media-plex:32400` |

- **Prowlarr:** add Sonarr, Radarr and Lidarr under *Settings → Apps* and it pushes your indexers to them.
- **Bazarr:** connect Sonarr and Radarr under *Settings*.
- **Root folders:** set `/data/media/tv`, `/data/media/movies` and so on in each app, and point your media server's libraries at the same paths.

`helm install` prints these addresses for your release.

## Exposing the apps

```yaml
ingress:
  enabled: true
  className: nginx
  domain: home.example.com        # sonarr.home.example.com, radarr.home.example.com...
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  tls:
    enabled: true
bazarr:
  ingress:
    enabled: false                # keep one app internal
sonarr:
  ingress:
    hostname: tv.example.com      # or override a hostname
```

**Turn on authentication in each app before exposing it.**

## Plex

```bash
kubectl create secret generic plex-claim -n media --from-literal=PLEX_CLAIM=claim-xxxxxxxx
helm upgrade media simplyzee/kube-arr -n media --reuse-values \
  --set mediaServer=plex --set plex.claimSecret.name=plex-claim
```

Get a claim token from [plex.tv/claim](https://plex.tv/claim). It expires after 4 minutes, so create the secret right before installing. Set `plex.advertiseUrl` to the address your devices use, and `plex.service.type: LoadBalancer` if you want it reachable on your network directly.

## Hardware transcoding

Pass the GPU through to your media server (Intel/AMD shown):

```yaml
jellyfin:
  extraVolumes:
    - name: dri
      hostPath:
        path: /dev/dri
  extraVolumeMounts:
    - name: dri
      mountPath: /dev/dri
  securityContext:
    privileged: true
```

## Parameters

### Global

| Parameter | Description | Default |
|---|---|---|
| `global.timezone` | Time zone for every app | `Etc/UTC` |
| `global.puid`, `global.pgid` | User and group that own files | `1000`, `1000` |
| `global.umask` | File creation mask | `002` |
| `global.colocate` | Keep all apps on one node (needed for ReadWriteOnce media) | `true` |
| `global.imagePullSecrets` | Pull secrets | `[]` |
| `global.podSecurityContext` | Pod security context | seccomp `RuntimeDefault` |
| `global.nodeSelector`, `global.tolerations` | Scheduling | `kubernetes.io/os: linux`, `[]` |
| `mediaServer` | `plex`, `jellyfin`, `emby` or `none` | `jellyfin` |

### Media volume

| Parameter | Description | Default |
|---|---|---|
| `media.mountPath` | Where every app sees the media volume | `/data` |
| `media.existingClaim` | Use an existing PVC | `""` |
| `media.nfs.server`, `media.nfs.path` | Use an NFS share | `""` |
| `media.hostPath.path` | Use a directory on the node | `""` |
| `media.hostPath.mountPropagation` | `HostToContainer` lets pods see FUSE mounts such as kube-rclone | `HostToContainer` |
| `media.persistence.size`, `.storageClass`, `.accessModes` | Chart-created PVC | `500Gi`, `""`, `[ReadWriteOnce]` |

### Ingress

| Parameter | Description | Default |
|---|---|---|
| `ingress.enabled` | Create an Ingress per app | `false` |
| `ingress.className` | Ingress class | `""` |
| `ingress.domain` | Apps are served at `<app>.<domain>` | `""` |
| `ingress.annotations` | e.g. cert-manager issuer | `{}` |
| `ingress.tls.enabled`, `.secretName` | TLS; empty secret name gives each app its own | `false`, `""` |

### Every app

`appDefaults` applies to every app, and each app block (`sonarr`, `radarr`, `lidarr`, `bazarr`, `prowlarr`, `jackett`, `plex`, `jellyfin`, `emby`) can override any of it.

| Parameter | Description | Default |
|---|---|---|
| `<app>.enabled` | Run the app (not for media servers; use `mediaServer`) | see table above |
| `<app>.image.repository`, `.tag`, `.pullPolicy` | Image | LinuxServer.io, pinned |
| `<app>.port` | HTTP port | app's standard port |
| `<app>.service.type`, `.annotations` | Service | `ClusterIP` |
| `<app>.ingress.enabled`, `.hostname` | Per-app Ingress switch and host override | `true`, `""` |
| `<app>.config.size`, `.storageClass`, `.existingClaim` | Config volume (kept on uninstall) | `2Gi` (media servers more) |
| `<app>.mountMedia`, `<app>.mediaReadOnly` | Media volume access | on, read-write (media servers read-only) |
| `<app>.env`, `.resources`, `.podAnnotations`, `.securityContext` | Container settings | empty |
| `<app>.extraVolumes`, `.extraVolumeMounts` | Extra volumes, e.g. a GPU | `[]` |
| `plex.claimToken` / `plex.claimSecret.name`, `.key` | Plex claim token (Secret preferred) | `""`, `PLEX_CLAIM` |
| `plex.advertiseUrl` | Address Plex advertises | `""` |
| `jellyfin.publishedServerUrl` | Address Jellyfin advertises | `""` |

## Uninstalling

`helm uninstall media -n media` removes the apps but keeps every volume, including your library and each app's settings, so reinstalling picks up where you left off. Delete the PVCs yourself to remove the data.
