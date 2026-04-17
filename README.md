# Overlayer

[![CI](https://github.com/cnrancher/overlayer/actions/workflows/ci.yaml/badge.svg)](https://github.com/cnrancher/overlayer/actions/workflows/ci.yaml)
[![GitHub Release](https://img.shields.io/github/v/release/cnrancher/overlayer?include_prereleases)](https://github.com/cnrancher/overlayer/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/cnrancher/overlayer)](https://goreportcard.com/report/github.com/cnrancher/overlayer)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Overlayer is a reverse proxy designed to reduce CDN traffic costs and provide security layer for registry servers.

```mermaid
flowchart TD
    User["Docker/Podman Client"]
    CDN["CDN"]
    Overlayer["Overlayer"]
    Manifest["Internal Registry URL<br/>(Manifest Index / Token)"]
    Blobs["Public Cloud Bucket<br/>(Sha256 Layers, Cached)"]

    User -->|Manifest / Token / Blobs|CDN
    CDN --> Overlayer

    Overlayer -->| Fetch Manifest / Login <br/>No cache, Real-Time | Manifest
    Overlayer -->|Fetch Large Layers <br/>Cached, reduces CDN traffic | Blobs
```

## Usage

```sh
git clone https://github.com/cnrancher/overlayer.git && cd overlayer
cp config.example.yaml config.yaml
# Edit configuration
vim config.yaml

# Optional: create CDN Auth Secret
echo "TokenValueFooBar" > token
podman secret create CDN_AUTH_TOKEN token

# Run in container
VERSION="latest"
podman run -dit \
    -v $(pwd)/config.yaml:/config.yaml \
    -v $(pwd)/certs:/certs \
    --name overlayer \
    --network host \
    --restart=always \
    --secret CDN_AUTH_TOKEN,type=env,target=BLOBS_CDN_AUTH_TOKEN \
    registry.rancher.cn/cnrancher/overlayer:${VERSION} run -c=/config.yaml
```

## License

Copyright 2026 SUSE Rancher

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
