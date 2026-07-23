<p align="center">
  <img src="docs/nimbus.png" alt="NimbusCore" width="280">
</p>

<h1 align="center">NimbusCore</h1>
<p align="center"><b>Orchestrateur de conteneurs nouvelle génération</b></p>
<p align="center">
  Un équivalent de Kubernetes repensé pour être plus sûr par défaut, plus efficace en ressources,
  et fonctionnellement plus complet — sans empiler dix outils tiers.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white">
  <img alt="Status" src="https://img.shields.io/badge/status-Phase%201%20scaffold-orange">
  <img alt="License" src="https://img.shields.io/badge/license-unset-lightgrey">
</p>

---

## À propos

NimbusCore reprend le modèle mental de Kubernetes — déclaratif, API-centrique, auto-réparateur —
en corrigeant ses angles morts connus : sécurité permissive par défaut, empilement d'outils tiers,
et overhead d'un control plane généraliste. Le design complet (architecture, sécurité, roadmap en 9
phases, stack technique) est dans
[`NimbusCore-document-de-conception.pdf`](./NimbusCore-document-de-conception.pdf).

Ce dépôt contient le code : nous en sommes au **cœur minimal de la Phase 1** (roadmap section 08) —
un control plane single-node capable de recevoir un `Deployment`, de le réconcilier en `Pod`s, et de
les exposer via gRPC.

## Architecture

```
                        CONTROL PLANE (cmd/nimbus-apiserver)
   ┌──────────────────────────────────────────────────────────────┐
   │  API Server (gRPC)        Controller Manager                 │
   │  ├─ PodService            └─ DeploymentReconciler             │
   │  ├─ NodeService               (désiré vs. observé, resync)    │
   │  └─ DeploymentService                                        │
   │              │                        │                      │
   │              └──────────► Store ◄─────┘                      │
   │                       (in-memory KV — Raft/BoltDB en Phase 2) │
   └──────────────────────────────────────────────────────────────┘
                              │ gRPC (mTLS en Phase 2)
                              ▼
                   Nœud (cmd/nimbus-agent)
                   agent ─ Runtime (CRI/containerd à venir)
```

- **`internal/store`** — abstraction `Store` (Get/Put/Delete/List) ; implémentation mémoire pour la
  Phase 1, remplacée par `hashicorp/raft` + BoltDB en Phase 2.
- **`internal/registry`** — accès typé et générique (`Registry[T proto.Message]`) au-dessus du
  `Store`, pour sérialiser/désérialiser les ressources Protobuf sans dupliquer le marshalling dans
  chaque service.
- **`internal/apiserver`** — serveur gRPC + implémentations de `PodService`, `NodeService`,
  `DeploymentService`.
- **`internal/controller`** — `Manager` + `Reconciler` (interface) et `DeploymentReconciler`
  (implémentation concrète : boucle de réconciliation à intervalle fixe, désiré vs. observé).
- **`internal/scheduler`** — `Scheduler` filter-then-score basique (pas encore branché : la Phase 1
  ne planifie pas encore sur plusieurs nœuds).
- **`internal/agent`** — squelette de l'agent de nœud, `Runtime` en attente du client CRI/containerd.
- **`api/v1`** — schéma Protobuf (`Pod`, `Node`, `Deployment` + services gRPC) et code généré.

## Démarrer

Prérequis : Go 1.25+.

```bash
# Control plane (API server + controller manager), écoute en gRPC sur :7443
go run ./cmd/nimbus-apiserver

# Agent de nœud (squelette, ne fait encore rien de fonctionnel)
go run ./cmd/nimbus-agent

# CLI
go run ./cmd/nimbusctl version
```

Lancer les tests (dont un test d'intégration bout-en-bout : création d'un `Deployment` par gRPC →
réconciliation → apparition des `Pod`s) :

```bash
go test ./...
```

## Régénérer le code Protobuf

Le schéma vit dans `api/v1/*.proto`. Après modification, régénérer avec `protoc` +
`protoc-gen-go` + `protoc-gen-go-grpc` :

```bash
protoc \
  --proto_path=api/v1 \
  --go_out=api/v1 --go_opt=paths=source_relative \
  --go-grpc_out=api/v1 --go-grpc_opt=paths=source_relative \
  api/v1/*.proto
```

## Stack technique

| Bibliothèque | Rôle |
|---|---|
| `google.golang.org/grpc`, `google.golang.org/protobuf` | Communication interne entre composants |
| `spf13/cobra` | CLI (`nimbusctl`) |
| `hashicorp/raft`, `hashicorp/raft-boltdb` | Consensus et stockage du control plane (Phase 2) |
| `containerd/containerd` | Client CRI, exécution des conteneurs (agent) |
| `spiffe/go-spiffe` | Identités et mTLS (Phase 2) |
| `cilium/ebpf`, `microsoft/ebpf-for-windows` | Dataplane réseau eBPF (Phase 4) |
| `sigstore/cosign` | Vérification de signature d'image (Phase 3) |

Les dépendances non encore importées (Raft, containerd, SPIFFE, eBPF, cosign) n'apparaissent pas
dans `go.mod` tant que le composant correspondant n'est pas réellement câblé — voir
[section 11 du design doc](./NimbusCore-document-de-conception.pdf) pour le détail.

## Roadmap

9 phases détaillées dans le design doc (section 08). État actuel :

- [x] **Phase 1 — Cœur minimal** : modèle Protobuf, store mono-nœud, API Server gRPC, boucle de
      réconciliation Deployment → Pod, CLI de base.
- [ ] **Phase 2 — Multi-nœuds** : Raft multi-nœuds, mTLS SPIFFE/SPIRE, scheduler branché, health
      checks.
- [ ] **Phase 3 — Sécurité renforcée** : admission control strict, signature d'image, seccomp/AppArmor,
      chiffrement au repos.
- [ ] Phases 4-9 : réseau/stockage avancés, self-healing, gouvernance native, mesh/GitOps,
      multi-cluster, FinOps — voir le design doc.
