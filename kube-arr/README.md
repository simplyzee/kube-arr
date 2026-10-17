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
| Host path | `media.hostPath.path: /mnt/disks/gdrive` | A disk on the node, or a kube-rclone you run separately |
| Cloud storage | `rclone.enabled: true` | Google Drive, S3, Dropbox and more, via the built-in [kube-rclone](https://github.com/simplyzee/kube-rclone) (see below) |

`global.colocate` (on by default) keeps every app on the same node, which a ReadWriteOnce volume needs. Turn it off with NFS or a ReadWriteMany volume to spread the apps across nodes.

Media servers mount the volume read-only. Prowlarr and Jackett don't mount it at all.

## Cloud storage with kube-rclone

kube-arr can run [kube-rclone](https://github.com/simplyzee/kube-rclone) for you. It mounts any [rclone remote](https://rclone.org/overview/) on every node, and every app uses that mount as its media volume.

1. Create an rclone config on your own machine with `rclone config`.
2. Install with rclone switched on:

```bash
helm install media simplyzee/kube-arr -n media --create-namespace \
  --set rclone.enabled=true \
  --set-file rclone.rclone.config=./rclone.conf \
  --set rclone.rclone.remote=gdrive:media
```

The remote is mounted at `/mnt/disks/rclone` on each node and appears as `/data` in every app. Your config is stored in a Secret; use `rclone.rclone.existingSecret` to manage it yourself instead.

The defaults suit the *arr apps:
- The mount is **read-write**, so imports and renames work.
- It uses the **full VFS cache** (`--vfs-cache-mode=full`, up to 50G on each node) so apps can seek and stream.
- Files are owned by **UID/GID 1000** with umask `002`.

If you change `global.puid`, `global.pgid` or `global.umask`, update `--uid`, `--gid` and `--umask` in `rclone.rclone.additionalArgs` to match. The install notes warn you if they don't.

Every [kube-rclone setting](https://github.com/simplyzee/kube-rclone/blob/master/kube-rclone/README.md#parameters) can be set under `rclone:`, for example `rclone.resources` or `rclone.rclone.cacheDir`.

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

## Transcode dispatch

Set `transcode.enabled=true` and Plex, Jellyfin or Emby stops transcoding inside its own pod. Each transcode runs as a separate, short-lived Kubernetes pod instead, with its own resources and scheduling, so a few 4K streams don't starve the server, and transcodes can go to a GPU node or a bigger machine. The idea comes from [kube-plex](https://github.com/munnerz/kube-plex), extended here to all three servers.

```bash
helm upgrade media simplyzee/kube-arr -n media --reuse-values \
  --set transcode.enabled=true
```

How it works:

- A small dispatcher, [`kube-arr-transcoder`](https://github.com/simplyzee/kube-arr/tree/main/transcoder), takes the place of the server's transcoder (`Plex Transcoder`, or `ffmpeg` for Jellyfin and Emby). The original is kept alongside it.
- When the server starts a real transcode, the dispatcher creates a pod from the server's own image with the same arguments, environment and working directory, mounting the server's config and media volumes, then streams the pod's output back so progress shows as normal.
- Quick capability checks (`-version`, `-encoders` and so on) and anything piped straight back to the server still run locally.
- Stopping playback stops the pod. If the dispatcher is killed outright, a watchdog deletes the pod anyway, and every pod is owned by the server pod so it's cleaned up with it.
- If a pod can't start in time (no capacity, image pull failure), the transcode runs locally instead. Set `transcode.fallbackToLocal=false` to turn that off.

Things to know:

- **Plex:** transcode pods report progress back over the network. In Plex, add your pod network (for example `10.0.0.0/8`) under *Settings > Network > List of IP addresses and networks that are allowed without auth*, or transcodes start and then stall.
- **Storage:** all three servers write transcodes into their config volume, so transcode pods mount it too. With ReadWriteOnce storage they run on the server's node (`transcode.sameNodeAsServer=true`, the default). Use ReadWriteMany storage for the server's config to send them anywhere, then set `sameNodeAsServer=false` and pick nodes with `transcode.nodeSelector`, `.tolerations` or `.affinity`.
- **GPUs:** transcode pods get the server's `extraVolumes`, `extraVolumeMounts` and `securityContext`, so the hardware setup below applies to them too. Add a GPU resource such as `gpu.intel.com/i915: 1` under `transcode.resources` if you use a device plugin.
- **Permissions:** the server gets a service account that can create, read and delete pods in its namespace, and nothing else. Without transcode dispatch no app mounts a service account token.

Watch transcodes come and go:

```bash
kubectl get pods -n media -l app.kubernetes.io/component=transcode -w
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

### Cloud storage (kube-rclone)

| Parameter | Description | Default |
|---|---|---|
| `rclone.enabled` | Run kube-rclone and use its mount as the media volume | `false` |
| `rclone.rclone.remote` | Remote (and path) from your rclone config | `gdrive:` |
| `rclone.rclone.path` | Where it's mounted on each node | `/mnt/disks/rclone` |
| `rclone.rclone.config` | rclone config contents (use `--set-file`) | `""` |
| `rclone.rclone.existingSecret` | Use your own Secret for the config instead | `""` |
| `rclone.rclone.readOnly` | Mount read-only | `false` |
| `rclone.rclone.additionalArgs` | rclone mount flags | VFS full cache, 50G, `--uid=1000 --gid=1000 --umask=002` |
| `rclone.*` | Any other kube-rclone setting | see kube-rclone |

### Transcode dispatch

| Parameter | Description | Default |
|---|---|---|
| `transcode.enabled` | Run the media server's transcodes as their own pods | `false` |
| `transcode.dispatcher.image.repository`, `.tag`, `.pullPolicy` | Dispatcher image; tag defaults to the chart version | `ghcr.io/simplyzee/kube-arr-transcoder`, `""` |
| `transcode.binary` | Transcoder path in the server image, if it's not the usual one | `""` |
| `transcode.fallbackToLocal` | Transcode locally if a pod can't start | `true` |
| `transcode.startTimeoutSeconds` | How long a pod may stay pending before falling back | `60` |
| `transcode.maxDurationSeconds` | Hard limit for one transcode pod | `21600` |
| `transcode.sameNodeAsServer` | Keep pods on the server's node (needed for ReadWriteOnce config) | `true` |
| `transcode.resources` | Transcode pod resources | 1 CPU, 1Gi requested; 4Gi limit |
| `transcode.nodeSelector`, `.tolerations`, `.affinity` | Transcode pod scheduling (`affinity` replaces `sameNodeAsServer`) | global settings |
| `transcode.podAnnotations`, `.env` | Extra annotations and environment for transcode pods | empty |

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
