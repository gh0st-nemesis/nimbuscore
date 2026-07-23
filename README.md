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
  <img alt="Status" src="https://img.shields.io/badge/status-Phase%203-orange">
  <img alt="License" src="https://img.shields.io/badge/license-unset-lightgrey">
</p>

---

## À propos

NimbusCore reprend le modèle mental de Kubernetes — déclaratif, API-centrique, auto-réparateur —
en corrigeant ses angles morts connus : sécurité permissive par défaut, empilement d'outils tiers,
et overhead d'un control plane généraliste. Le design complet (architecture, sécurité, roadmap en 9
phases, stack technique) est dans
[`NimbusCore-document-de-conception.pdf`](./NimbusCore-document-de-conception.pdf).

Ce dépôt contient le code : **Phase 1 (cœur minimal)**, **Phase 2 (multi-nœuds)** et **Phase 3
(sécurité renforcée)** de la roadmap (section 08) sont implémentées — un cluster réel, multi-processus,
avec consensus Raft, identités mTLS, admission control non contournable, RBAC deny-by-default et
chiffrement au repos.

## Architecture

```
        CONTROL PLANE — une ou plusieurs réplicas (cmd/nimbus-apiserver)
   ┌────────────────────────────────────────────────────────────────────┐
   │  gRPC (mTLS SPIFFE) + AuthInterceptor (RBAC deny-by-default)       │
   │  ├─ IdentityService   (enrôlement — CA uniquement sur le bootstrap)│
   │  ├─ AdminService      (JoinRaft — réplicas control-plane only)     │
   │  ├─ PodService / DeploymentService ──► Admission Chain             │
   │  │        ├─ SecurityContextPolicy (seccomp, capabilities, privileged)
   │  │        ├─ ImageSignaturePolicy  (signature ECDSA obligatoire)   │
   │  │        └─ QuotaPolicy           (CPU/mémoire par namespace)     │
   │  └─ NodeService                                                    │
   │                                                                    │
   │  Controller Manager (actif sur le leader Raft uniquement)         │
   │  ├─ DeploymentReconciler   (désiré vs. observé + scheduler)        │
   │  └─ NodeHealthReconciler   (heartbeat expiré → not-ready)          │
   │              │                                                    │
   │              └──────────► RaftStore (BoltDB, chiffré AES-256-GCM) │
   └────────────────────────────────────────────────────────────────────┘
              ▲ Raft (réplication)          ▲ gRPC mTLS (enrôlement, heartbeat)
              │                             │
     autre réplica control-plane       Nœud (cmd/nimbus-agent)
                                       enrôle → SVID, s'enregistre comme Node,
                                       heartbeat périodique, Runtime (CRI à venir)
```

- **`internal/store`** — `Store` (Get/Put/Delete/List). `memStore` (dev, non durable) et `RaftStore`
  (`hashicorp/raft` + `raft-boltdb`, répliqué, **chiffré au repos en AES-256-GCM** — la clé de
  chiffrement du cluster est distribuée via l'enrôlement, jamais en clair sur le disque).
- **`internal/identity`** — CA légère en mémoire qui émet des SVID X.509 SPIFFE, enrôlement
  trust-on-first-use, `tls.Config` client/serveur.
- **`internal/rbac`** — `Role`/`Rule`/`Binding`/`Authorizer` : autorisation deny-by-default par
  préfixe de chemin SPIFFE (`/node/`, `/client/`, `/control-plane/`).
- **`internal/admission`** — pipeline non contournable branché sur `CreatePod`/`CreateDeployment` :
  `SecurityContextPolicy` (rejette les conteneurs privilégiés, défaut le profil seccomp, filtre les
  capacités Linux), `ImageSignaturePolicy` (signature obligatoire), `QuotaPolicy` (quota CPU/mémoire
  par namespace, tient compte des réplicas).
- **`internal/imagesign`** — signature/vérification ECDSA d'une référence d'image + fichier de
  confiance JSON. Remplace cosign/sigstore — voir plus bas.
- **`internal/registry`** — accès typé et générique (`Registry[T proto.Message]`) au-dessus du `Store`.
- **`internal/apiserver`** — serveur gRPC, `AuthInterceptor` (identité + RBAC), les services
  `Identity`/`Admin`/`Pod`/`Node`/`Deployment`.
- **`internal/controller`** — `Manager`/`Reconciler`, `DeploymentReconciler`, `NodeHealthReconciler`,
  `RunWhileLeader`.
- **`internal/scheduler`** — `Scheduler` filter-then-score.
- **`internal/agent`** — agent de nœud ; `Runtime` en attente du client CRI/containerd.
- **`api/v1`** — schéma Protobuf et code généré.

## Démarrer

Prérequis : Go 1.25+, `protoc` (uniquement pour régénérer le schéma).

### 1. Générer une clé de signature d'image et signer une image

```bash
go run ./cmd/nimbusctl generate-key --out-dir=./keys
go run ./cmd/nimbusctl sign-image "nginx:v1" --key=./keys/image-signing-key.pem --trust-file=./trust.json
```

### 2. Démarrer le control plane (bootstrap)

```bash
go run ./cmd/nimbus-apiserver -bootstrap -join-token=devtoken \
  -image-signing-pubkey=./keys/image-signing-key.pub.pem -image-trust-file=./trust.json \
  -namespace-cpu-quota-millis=4000 -namespace-memory-quota-bytes=4294967296
```

Sans `-image-signing-pubkey`/`-image-trust-file`, le serveur refuse de démarrer — sauf si
`-insecure-skip-image-verification` est explicitement passé (dev uniquement, imprime un avertissement).
Sans clé de chiffrement fournie (cas mono-nœud/dev sans enrôlement d'un second réplica), le store
avertit et stocke en clair ; en pratique la clé est toujours générée au bootstrap.

### 3. Démarrer un agent

```bash
go run ./cmd/nimbus-agent -node-name=worker-1 -control-plane-addr=127.0.0.1:7443 -join-token=devtoken
```

### 4. Cluster multi-nœuds sur une machine (Phase 2)

```bash
go run ./cmd/nimbus-apiserver -node-id=node-2 -api-addr=127.0.0.1:7444 -raft-addr=127.0.0.1:7947 \
  -data-dir=./data/node-2 -join-token=devtoken -join-api-addr=127.0.0.1:7443
```

Couper `nimbus-agent` : après ~15s sans heartbeat, le nœud passe `not-ready` et ses Pods sont
réévacués sur un nœud sain.

### Identités et permissions (RBAC)

Trois rôles SVID, choisis à l'enrôlement (`SVIDRole`) :

| Rôle | Chemin SPIFFE | Permissions |
|---|---|---|
| `NODE` | `/node/<name>` | `nodes:create,update` uniquement (auto-enregistrement + heartbeat) |
| `CLIENT` | `/client/<name>` | `pods:*`, `deployments:*`, `nodes:get,list` — humains/CI/nimbusctl |
| `CONTROL_PLANE` | `/control-plane/<id>` | `*:*` — réplicas du control plane uniquement |

Tout appel dont la méthode n'a pas de mapping RBAC explicite, ou dont l'identité n'a pas de binding
correspondant, est **refusé par défaut**.

> **Limites connues (honnêteté technique)** :
> - Seul le réplica démarré avec `-bootstrap` détient la clé privée de la CA et sert
>   `IdentityService` : l'enrôlement doit cibler cette adresse-là spécifiquement. Une CA hautement
>   disponible (vrai SPIRE, ou clé de CA répliquée par Raft) est un travail de phase ultérieure.
> - Un client doit pointer vers le leader Raft courant pour écrire (`ErrNotLeader` sinon) — pas de
>   redirection automatique.
> - `internal/imagesign` vérifie une signature ECDSA locale sur la référence d'image, pas une vraie
>   signature cosign/sigstore attachée dans un registre OCI : ce dernier nécessite un registre et
>   l'infrastructure Rekor/Fulcio de Sigstore, indisponibles hors-ligne ici, et sa bibliothèque Go
>   (`sigstore/cosign`) tire une arborescence de dépendances considérable (rekor, fulcio, tuf,
>   go-containerregistry). Même modèle de confiance (vérifier une signature avant d'admettre),
>   sans la dépendance registre — un vrai vérificateur cosign implémente la même interface
>   `admission.ImageVerifier` et peut le remplacer directement.
> - Les profils seccomp/AppArmor sont défaultés et validés au niveau du schéma/admission ; leur
>   application effective au runtime attend le câblage CRI/containerd de l'agent (Phase 3+).

Lancer les tests (Raft mono-nœud réel, chiffrement au repos vérifié sur le fichier BoltDB, handshake
mTLS réel, admission control + RBAC testés bout-en-bout par gRPC) :

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
| `hashicorp/raft`, `hashicorp/raft-boltdb` | Consensus et stockage répliqué, chiffré au repos |
| `spiffe/go-spiffe` (`spiffeid`) | Parsing/formatage/matching des identités SPIFFE — la CA elle-même est maison (pas de SPIRE server) |
| `crypto/ecdsa`, `crypto/aes` (stdlib) | Signature d'image et chiffrement au repos — stand-ins pour cosign/sigstore |
| `containerd/containerd` | Client CRI, exécution des conteneurs (agent — Phase 4+) |
| `cilium/ebpf`, `microsoft/ebpf-for-windows` | Dataplane réseau eBPF (Phase 4) |

Les dépendances non encore importées (containerd, eBPF) n'apparaissent pas dans `go.mod` tant que le
composant correspondant n'est pas réellement câblé — voir
[section 11 du design doc](./NimbusCore-document-de-conception.pdf) pour le détail.

## Roadmap

9 phases détaillées dans le design doc (section 08). État actuel :

- [x] **Phase 1 — Cœur minimal** : modèle Protobuf, store mono-nœud, API Server gRPC, boucle de
      réconciliation Deployment → Pod, CLI de base.
- [x] **Phase 2 — Multi-nœuds** : Raft multi-nœuds (tolérance de panne), mTLS SPIFFE, scheduler
      branché, health checks + éviction des pods d'un nœud mort.
- [x] **Phase 3 — Sécurité renforcée** : admission control non contournable (signature d'image,
      contexte de sécurité, quotas), chiffrement au repos AES-256-GCM, RBAC deny-by-default.
- [ ] Phases 4-9 : réseau/stockage avancés (eBPF, CSI), self-healing enrichi, gouvernance native,
      mesh/GitOps, multi-cluster, FinOps — voir le design doc.
