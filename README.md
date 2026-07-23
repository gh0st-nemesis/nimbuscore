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
  <img alt="Status" src="https://img.shields.io/badge/status-Phase%205-orange">
  <img alt="License" src="https://img.shields.io/badge/license-unset-lightgrey">
</p>

---

## À propos

NimbusCore reprend le modèle mental de Kubernetes — déclaratif, API-centrique, auto-réparateur —
en corrigeant ses angles morts connus : sécurité permissive par défaut, empilement d'outils tiers,
et overhead d'un control plane généraliste. Le design complet (architecture, sécurité, roadmap en 9
phases, stack technique) est dans
[`NimbusCore-document-de-conception.pdf`](./NimbusCore-document-de-conception.pdf).

Ce dépôt contient le code : **Phases 1 à 5** de la roadmap (section 08) sont implémentées — un cluster
réel, multi-processus, avec consensus Raft, identités mTLS, admission control non contournable, RBAC
deny-by-default, chiffrement au repos, stockage persistant via CSI, politiques réseau deny-by-default,
télémétrie OpenTelemetry native, et — nouveau en Phase 5 — un agent qui exécute réellement des
processus, redémarre ceux qui crashent, évince/replace ceux en échec terminal, et un autoscaler
horizontal qui ajuste le nombre de réplicas sur la mémoire réellement observée.

## Architecture

```
        CONTROL PLANE — une ou plusieurs réplicas (cmd/nimbus-apiserver)
   ┌────────────────────────────────────────────────────────────────────┐
   │  gRPC (mTLS SPIFFE) + AuthInterceptor (RBAC) + otelgrpc (traces)   │
   │  ├─ IdentityService / AdminService                                 │
   │  ├─ PodService / DeploymentService ──► Admission Chain             │
   │  │        ├─ SecurityContextPolicy / ImageSignaturePolicy / Quota  │
   │  ├─ NodeService                                                    │
   │  ├─ VolumeService ──► driver CSI hostpath (Controller/Node/Identity)│
   │  └─ NetworkPolicyService ──► internal/netpolicy (moteur de policy) │
   │                                                                    │
   │  Controller Manager (actif sur le leader Raft uniquement)         │
   │  ├─ DeploymentReconciler   (désiré vs. observé + scheduler +       │
   │  │    éviction nœud mort/pod en échec terminal)                   │
   │  ├─ NodeHealthReconciler   (heartbeat expiré → not-ready)          │
   │  └─ HorizontalAutoscaler   (replicas ajustés sur mémoire observée) │
   │              │                                                    │
   │              └──────────► RaftStore (BoltDB, chiffré AES-256-GCM) │
   └────────────────────────────────────────────────────────────────────┘
              ▲ Raft (réplication)          ▲ gRPC mTLS (enrôlement, heartbeat,
              │                             │  UpdatePodStatus)
     autre réplica control-plane       Nœud (cmd/nimbus-agent)
                                       enrôle → SVID, s'enregistre comme Node,
                                       heartbeat périodique, boucle de
                                       supervision : exécute chaque conteneur
                                       comme process OS réel, détecte les
                                       sorties, redémarre selon RestartPolicy,
                                       rapporte phase + mémoire réelle (RSS)
```

- **`internal/store`** — `Store` (Get/Put/Delete/List). `RaftStore` répliqué, chiffré au repos.
- **`internal/identity`** — CA légère SPIFFE, enrôlement trust-on-first-use.
- **`internal/rbac`** — autorisation deny-by-default par préfixe de chemin SPIFFE.
- **`internal/admission`** — pipeline non contournable (sécurité, signature, quotas).
- **`internal/imagesign`** — signature/vérification ECDSA — stand-in pour cosign/sigstore.
- **`internal/csi/hostpath`** — driver CSI réel (Identity/Controller/Node) qui provisionne des volumes
  comme répertoires sur disque — implémentation de référence, dans l'esprit du hostpath-driver que
  Kubernetes utilise pour le dev/test, prouvant la compatibilité CSI (`Volume`/`VolumeService`).
- **`internal/netpolicy`** — moteur de policy réseau : `Allowed(policies, source, dest, protocole,
  port)`, **deny-by-default** même en l'absence de toute policy (contrairement à Kubernetes, où un pod
  non sélectionné reste ouvert par défaut — différenciateur voulu, section 01 du design doc).
- **`internal/telemetry`** — OpenTelemetry natif : traces automatiques de chaque appel gRPC
  (`otelgrpc`), métriques (décisions d'admission), exporteurs `none`/`stdout`/`otlp`.
- **`internal/registry`** — accès typé et générique au-dessus du `Store`.
- **`internal/apiserver`** — serveur gRPC, `AuthInterceptor`, tous les services de ressources.
- **`internal/controller`** — `Manager`/`Reconciler`, réconciliateurs (`DeploymentReconciler`,
  `NodeHealthReconciler`, `HorizontalAutoscaler`), `RunWhileLeader`. Le `DeploymentReconciler`
  planifie sur des métriques d'usage réelles (somme des requêtes CPU/mémoire déjà attribuées par
  nœud, pas des valeurs figées) et remplace tout pod en phase terminale (`Succeeded`/`Failed`), en plus
  d'évincer ceux d'un nœud mort.
- **`internal/scheduler`** — `Scheduler` filter-then-score.
- **`internal/agent`** — agent de nœud : boucle de réconciliation réelle (liste les pods qui lui sont
  assignés, démarre/arrête de vrais processus OS via `os/exec`, détecte les sorties, redémarre selon
  `RestartPolicy`, échantillonne la mémoire résidente réelle via `gopsutil`, rapporte tout via
  `PodService.UpdatePodStatus`). Ce n'est pas encore un vrai runtime OCI/CRI (pas d'image, pas
  d'isolation cgroups/namespaces) — `Container.command` exécute directement un binaire côté hôte ;
  voir les limites connues plus bas.
- **`api/v1`** — schéma Protobuf (`Pod`, `Node`, `Deployment`, `Volume`, `NetworkPolicy`, services
  d'identité/admin) et code généré.

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
  -namespace-cpu-quota-millis=4000 -namespace-memory-quota-bytes=4294967296 \
  -otel-exporter=stdout
```

`-otel-exporter` accepte `none` (défaut), `stdout` (traces/métriques imprimées localement) ou `otlp`
(`-otlp-endpoint` pour pointer vers un collecteur). `-csi-hostpath-dir` choisit où le driver CSI
intégré provisionne ses volumes (répertoires sur disque, défaut `./data/volumes`).

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

### Self-healing et autoscaling (Phase 5)

Un `Container.command` (ex. `["powershell.exe", "-Command", "Start-Sleep -Seconds 60"]` sur Windows,
`["sleep", "60"]` sur Linux/macOS) est exécuté tel quel par l'agent comme process OS. Avec
`RestartPolicy: RESTART_POLICY_ALWAYS`, un process qui crashe est automatiquement relancé —
`status.restart_count` grimpe à chaque tentative. Un `Deployment` avec `min_replicas`/`max_replicas`
renseignés est piloté par l'`HorizontalAutoscaler` : le nombre de réplicas est ajusté toutes les 15s
sur la mémoire résidente réellement mesurée (`status.memory_usage_bytes`) rapportée à la mémoire
demandée par le conteneur — mêmes formules que l'algorithme HPA de Kubernetes (cible 100% d'utilisation
par défaut).

### Benchmarks

Des benchmarks Go mesurent les opérations internes du control plane (boucle de réconciliation,
pipeline d'admission, décision du scheduler, lecture/écriture du store) :

```bash
go test ./internal/controller/... ./internal/admission/... ./internal/scheduler/... ./internal/store/... -bench=. -run=^$
```

> Il ne s'agit pas d'un benchmark comparatif contre un vrai cluster Kubernetes — aucun cluster K8s
> n'est disponible dans cet environnement de développement hors-ligne. Ce sont des mesures réelles,
> mais uniquement de notre propre système.

### Identités et permissions (RBAC)

Trois rôles SVID, choisis à l'enrôlement (`SVIDRole`) :

| Rôle | Chemin SPIFFE | Permissions |
|---|---|---|
| `NODE` | `/node/<name>` | `nodes:create,update` uniquement (auto-enregistrement + heartbeat) |
| `CLIENT` | `/client/<name>` | `pods:*`, `deployments:*`, `volumes:*`, `networkpolicies:*`, `nodes:get,list` |
| `CONTROL_PLANE` | `/control-plane/<id>` | `*:*` — réplicas du control plane uniquement |

Tout appel dont la méthode n'a pas de mapping RBAC explicite, ou dont l'identité n'a pas de binding
correspondant, est **refusé par défaut**.

> **Limites connues (honnêteté technique)** :
> - Seul le réplica démarré avec `-bootstrap` détient la clé privée de la CA et sert
>   `IdentityService`. Un client doit pointer vers le leader Raft courant pour écrire.
> - `internal/imagesign` vérifie une signature ECDSA locale, pas une vraie signature cosign/sigstore
>   attachée dans un registre OCI (celle-ci nécessite un registre + l'infra Rekor/Fulcio, indisponibles
>   hors-ligne ici, et sa bibliothèque Go tire une arborescence de dépendances considérable). Même
>   modèle de confiance, sans la dépendance registre.
> - Les profils seccomp/AppArmor sont défaultés et validés au niveau du schéma/admission ; leur
>   application effective au runtime attend le câblage CRI/containerd de l'agent.
> - Le driver CSI hostpath provisionne de vrais répertoires et les publie via un lien symbolique
>   (`NodePublishVolume`) ; la création de symlinks sans privilège élevé échoue sur certaines
>   configurations Windows (le test correspondant se `Skip`s proprement dans ce cas) — fonctionne sans
>   souci sur Linux/macOS. Il n'impose pas non plus de quota disque réel au niveau filesystem.
> - `internal/netpolicy` est le moteur de décision (la « brain » qu'un vrai dataplane consulterait),
>   pas une application au niveau noyau : eBPF a besoin d'un noyau Linux pour charger des programmes.
>   Le moteur `Allowed(...)` est prêt à être branché derrière `cilium/ebpf` dès que l'agent isole
>   réellement les conteneurs dans des espaces réseau (namespaces Linux / Job Objects Windows).
> - L'agent exécute les conteneurs comme de simples processus OS (`os/exec`), pas comme de vrais
>   conteneurs OCI : pas d'image téléchargée, pas d'isolation cgroups/namespaces/Job Objects, pas de
>   système de fichiers dédié. `Container.command` doit pointer vers un binaire déjà présent sur le
>   nœud. C'est un runtime de développement qui rend le self-healing et l'autoscaling réels et
>   testables dès maintenant ; le vrai câblage CRI/containerd (Phase 4 du design doc, toujours en
>   attente) remplacera ce runtime par l'exécution de vraies images de conteneurs, sans changer
>   l'interface `agent.Runtime` ni la logique de réconciliation qui en dépend.
> - Si l'agent est tué de façon non-gracieuse (ex. `kill -9`, coupure de courant), les processus qu'il
>   supervisait restent orphelins — l'agent ne s'appuie pas encore sur les cgroups Linux ou les Job
>   Objects Windows pour garantir leur nettoyage automatique (le design doc note déjà cette différence
>   de modèle d'isolation entre Linux et Windows, section 10).
> - L'`HorizontalAutoscaler` et le `DeploymentReconciler` écrivent des champs différents du même objet
>   `Deployment` (`.Spec.Replicas` vs. `.Status`) de manière concurrente ; chacun relit l'objet juste
>   avant d'écrire pour éviter de s'écraser mutuellement (pas de vérification `resource_version`
>   complète à travers tout le système pour l'instant — un seul champ protégé par écrivain).

Lancer les tests (Raft mono-nœud réel, chiffrement au repos vérifié sur le fichier BoltDB, handshake
mTLS réel, admission control + RBAC testés bout-en-bout par gRPC, spans OpenTelemetry vérifiés en
mémoire, driver CSI qui crée/liste/supprime de vrais répertoires) :

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
| `spiffe/go-spiffe` (`spiffeid`) | Parsing/formatage/matching des identités SPIFFE — la CA elle-même est maison |
| `crypto/ecdsa`, `crypto/aes` (stdlib) | Signature d'image et chiffrement au repos — stand-ins pour cosign/sigstore |
| `container-storage-interface/spec` | Contrat CSI standard (driver hostpath maison) |
| `go.opentelemetry.io/otel`, `.../contrib/.../otelgrpc` | Traces et métriques natives, sans agent tiers |
| `containerd/containerd` | Client CRI, exécution des conteneurs (agent — travail futur) |
| `cilium/ebpf`, `microsoft/ebpf-for-windows` | Dataplane réseau eBPF (à câbler une fois le CRI en place) |

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
- [x] **Phase 4 — Réseau et stockage avancés** : driver CSI hostpath réel, moteur de policy réseau
      deny-by-default, télémétrie OpenTelemetry native (traces gRPC automatiques + métriques).
- [x] **Phase 5 — Auto-guérison et efficacité** : agent à supervision de processus réelle
      (`RestartPolicy`, `restart_count`), remplacement des pods en échec terminal, scheduler informé
      par l'usage réellement attribué par nœud, autoscaling horizontal sur mémoire observée,
      benchmarks du control plane.
- [ ] Phases 6-9 : gouvernance/observabilité natives supplémentaires (coffre-fort de secrets,
      policy engine), mesh/GitOps natifs, multi-cluster/edge/WASM/GPU, FinOps — voir le design doc.
