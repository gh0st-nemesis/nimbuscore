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
  <img alt="Status" src="https://img.shields.io/badge/status-Phase%202-orange">
  <img alt="License" src="https://img.shields.io/badge/license-unset-lightgrey">
</p>

---

## À propos

NimbusCore reprend le modèle mental de Kubernetes — déclaratif, API-centrique, auto-réparateur —
en corrigeant ses angles morts connus : sécurité permissive par défaut, empilement d'outils tiers,
et overhead d'un control plane généraliste. Le design complet (architecture, sécurité, roadmap en 9
phases, stack technique) est dans
[`NimbusCore-document-de-conception.pdf`](./NimbusCore-document-de-conception.pdf).

Ce dépôt contient le code : **Phase 1 (cœur minimal)** et **Phase 2 (multi-nœuds)** de la roadmap
(section 08) sont implémentées — un cluster réel, multi-processus, avec consensus Raft, identités
mTLS, scheduler branché et tolérance à la panne d'un nœud.

## Architecture

```
        CONTROL PLANE — une ou plusieurs réplicas (cmd/nimbus-apiserver)
   ┌────────────────────────────────────────────────────────────────────┐
   │  gRPC (mTLS SPIFFE)                                                │
   │  ├─ IdentityService   (enrôlement — CA uniquement sur le bootstrap)│
   │  ├─ AdminService      (JoinRaft — réplicas control-plane only)     │
   │  ├─ PodService / NodeService / DeploymentService                  │
   │                                                                    │
   │  Controller Manager (actif sur le leader Raft uniquement)         │
   │  ├─ DeploymentReconciler   (désiré vs. observé + scheduler)        │
   │  └─ NodeHealthReconciler   (heartbeat expiré → not-ready)          │
   │              │                                                    │
   │              └──────────► RaftStore (BoltDB, répliqué) ◄──────────┤
   └────────────────────────────────────────────────────────────────────┘
              ▲ Raft (réplication)          ▲ gRPC mTLS (enrôlement, heartbeat)
              │                             │
     autre réplica control-plane       Nœud (cmd/nimbus-agent)
                                       enrôle → SVID, s'enregistre comme Node,
                                       heartbeat périodique, Runtime (CRI à venir)
```

- **`internal/store`** — abstraction `Store` (Get/Put/Delete/List). `memStore` (Phase 1, mono-nœud,
  non durable) et `RaftStore` (Phase 2 : `hashicorp/raft` + `raft-boltdb`, répliqué, tolérant à la
  panne d'une minorité de réplicas).
- **`internal/identity`** — CA légère en mémoire qui émet des SVID X.509 avec un SAN URI
  `spiffe://<trust-domain>/...` (rotation, pas de credentials longue durée). Remplace un vrai serveur
  SPIRE qui n'existe pas dans cet environnement — voir le commentaire de package pour le détail du
  compromis. Fournit aussi l'enrôlement (`Enroll`, trust-on-first-use) et les `tls.Config` client/serveur.
- **`internal/registry`** — accès typé et générique (`Registry[T proto.Message]`) au-dessus du
  `Store`.
- **`internal/apiserver`** — serveur gRPC, `AuthInterceptor` (deny-by-default sur l'identité),
  `IdentityService`, `AdminService`, `PodService`, `NodeService`, `DeploymentService`.
- **`internal/controller`** — `Manager`/`Reconciler`, `DeploymentReconciler` (scale up/down, appelle
  le `Scheduler`, évince les pods d'un nœud mort), `NodeHealthReconciler`, et `RunWhileLeader` (les
  boucles ne tournent que sur le leader Raft).
- **`internal/scheduler`** — `Scheduler` filter-then-score, branché dans `DeploymentReconciler`.
- **`internal/agent`** — agent de nœud ; `Runtime` en attente du client CRI/containerd.
- **`api/v1`** — schéma Protobuf (`Pod`, `Node`, `Deployment`, `IdentityService`, `AdminService` + les
  services de ressources) et code généré.

## Démarrer

Prérequis : Go 1.25+, `protoc` (uniquement pour régénérer le schéma).

### Mono-nœud (Phase 1)

```bash
go run ./cmd/nimbus-apiserver -bootstrap -join-token=devtoken
go run ./cmd/nimbus-agent -control-plane-addr=127.0.0.1:7443 -join-token=devtoken
go run ./cmd/nimbusctl version
```

### Cluster multi-nœuds sur une machine (Phase 2)

```bash
# Réplica 1 — bootstrap : crée la CA et devient le seul votant Raft
go run ./cmd/nimbus-apiserver -bootstrap -node-id=node-1 \
  -api-addr=127.0.0.1:7443 -raft-addr=127.0.0.1:7946 \
  -data-dir=./data/node-1 -join-token=devtoken

# Réplica 2 — rejoint via node-1 (qui détient la CA et est le leader initial)
go run ./cmd/nimbus-apiserver -node-id=node-2 \
  -api-addr=127.0.0.1:7444 -raft-addr=127.0.0.1:7947 \
  -data-dir=./data/node-2 -join-token=devtoken -join-api-addr=127.0.0.1:7443

# Un nœud worker — s'enrôle, s'enregistre comme Node, heartbeat toutes les 5s
go run ./cmd/nimbus-agent -node-name=worker-1 \
  -control-plane-addr=127.0.0.1:7443 -join-token=devtoken
```

Couper `nimbus-agent` (Ctrl+C ou `kill`) : après ~15s sans heartbeat, `node-health-controller` marque
le nœud `not-ready` et `deployment-controller` réévacue et recrée ses Pods sur un nœud sain — c'est le
test qu'on a fait à la main pour valider "tolérant à la panne d'un nœud".

> **Limite connue (honnêteté technique)** : seul le réplica démarré avec `-bootstrap` détient la clé
> privée de la CA et sert `IdentityService`. L'enrôlement (nœuds et réplicas suivants) doit donc
> cibler cette adresse-là spécifiquement, pas n'importe quel réplica. Une CA hautement disponible
> (vrai SPIRE, ou clé de CA elle-même répliquée par Raft) est un travail de phase ultérieure. De même,
> un client doit pointer vers le leader Raft courant pour écrire (`ErrNotLeader` sinon) — pas de
> redirection automatique pour l'instant.

Lancer les tests (dont un Raft mono-nœud réel, un handshake mTLS réel, et un test d'intégration
bout-en-bout : `Deployment` créé par gRPC → scheduler → réconciliation → `Pod`s programmés) :

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
| `hashicorp/raft`, `hashicorp/raft-boltdb` | Consensus et stockage répliqué du control plane |
| `spiffe/go-spiffe` (`spiffeid`) | Parsing/formatage/matching des identités SPIFFE — la CA elle-même est maison (pas de SPIRE server dans cet environnement) |
| `containerd/containerd` | Client CRI, exécution des conteneurs (agent — Phase 3+) |
| `cilium/ebpf`, `microsoft/ebpf-for-windows` | Dataplane réseau eBPF (Phase 4) |
| `sigstore/cosign` | Vérification de signature d'image (Phase 3) |

Les dépendances non encore importées (containerd, eBPF, cosign) n'apparaissent pas dans `go.mod` tant
que le composant correspondant n'est pas réellement câblé — voir
[section 11 du design doc](./NimbusCore-document-de-conception.pdf) pour le détail.

## Roadmap

9 phases détaillées dans le design doc (section 08). État actuel :

- [x] **Phase 1 — Cœur minimal** : modèle Protobuf, store mono-nœud, API Server gRPC, boucle de
      réconciliation Deployment → Pod, CLI de base.
- [x] **Phase 2 — Multi-nœuds** : Raft multi-nœuds (tolérance de panne), mTLS SPIFFE (CA maison, faute
      de SPIRE), scheduler branché, health checks + éviction des pods d'un nœud mort.
- [ ] **Phase 3 — Sécurité renforcée** : admission control strict, signature d'image, seccomp/AppArmor,
      chiffrement au repos, RBAC fin (aujourd'hui : simple appartenance au trust domain).
- [ ] Phases 4-9 : réseau/stockage avancés, self-healing enrichi, gouvernance native, mesh/GitOps,
      multi-cluster, FinOps — voir le design doc.
