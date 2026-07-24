<p align="center">
  <img src="docs/nimbus.png" alt="NimbusCore" width="280">
</p>

<h1 align="center">NimbusCore</h1>
<p align="center"><b>Orchestrateur de conteneurs, en Go, façon Kubernetes</b></p>

<p align="center">
  <a href="https://github.com/gh0st-nemesis/nimbuscore/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/gh0st-nemesis/nimbuscore?label=release&color=blue"></a>
  <img alt="Go" src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white">
  <img alt="Status" src="https://img.shields.io/badge/status-stable-brightgreen">
  <img alt="Platform" src="https://img.shields.io/badge/platform-linux%2Famd64-lightgrey">
  <img alt="Docker" src="https://img.shields.io/badge/requires-Docker-2496ED?logo=docker&logoColor=white">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-blue.svg"></a>
</p>

---

## Installer

Un `.deb` autonome (aucun besoin de Go ni de git sur la machine cible) :

```bash
curl -LO https://github.com/gh0st-nemesis/nimbuscore/releases/latest/download/nimbuscore_0.1.2_amd64.deb
sudo dpkg -i nimbuscore_0.1.2_amd64.deb
```

Ça installe `nimbus-apiserver`, `nimbus-agent` et `nimbusctl`, plus les services systemd correspondants.
Docker doit être installé séparément sur les nœuds qui font tourner des pods :

```bash
sudo apt install -y docker.io
```

## Démarrer un cluster

Sur la machine qui sera le control plane :

```bash
sudo nano /etc/nimbuscore/apiserver.env   # remplacer CHANGEME par un vrai join-token
sudo systemctl enable --now nimbus-apiserver
```

Sur chaque machine qui exécutera des pods :

```bash
sudo nano /etc/nimbuscore/agent.env       # node-name, control-plane-addr, même join-token
sudo systemctl enable --now nimbus-agent
```

Configurer le client :

```bash
nimbusctl config set-context --control-plane-addr=<ip-du-control-plane>:7443 --join-token=<ton-token>
nimbusctl get nodes
```

## Exemple

Lancer un vrai conteneur nginx et le rendre joignable :

```bash
nimbusctl run web --image=nginx:alpine --port=80

cat > web-svc.json <<'EOF'
{"kind":"Service","metadata":{"name":"web-svc","namespace":"default"},
 "spec":{"selector":{"app":"web"},"port":80,"target_port":80}}
EOF
nimbusctl apply -f web-svc.json

nimbusctl get services
# NAME     NAMESPACE  PORT  ENDPOINTS
# web-svc  default    80    <ip-du-noeud>:30000   <- port généré automatiquement

curl http://<ip-du-noeud>:30000/
```

Autres commandes utiles :

```bash
nimbusctl get pods
nimbusctl get deployments
nimbusctl delete pod web
nimbusctl run api --image=alpine:v1 --replicas=3 -- sleep 3600   # un Deployment
```
