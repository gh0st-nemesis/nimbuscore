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
  <img alt="Status" src="https://img.shields.io/badge/status-Phase%209-brightgreen">
  <img alt="License" src="https://img.shields.io/badge/license-unset-lightgrey">
</p>

---

## À propos

NimbusCore reprend le modèle mental de Kubernetes — déclaratif, API-centrique, auto-réparateur —
en corrigeant ses angles morts connus : sécurité permissive par défaut, empilement d'outils tiers,
et overhead d'un control plane généraliste. 

Ce dépôt contient le code : **les 9 phases** de la roadmap (section 08) sont implémentées — un cluster
réel, multi-processus, avec consensus Raft, identités mTLS, admission control non contournable, RBAC
deny-by-default, chiffrement au repos, stockage persistant via CSI, politiques réseau deny-by-default,
télémétrie OpenTelemetry native, un agent qui exécute réellement des processus (redémarre ceux qui
crashent, évince/remplace ceux en échec terminal), un autoscaler horizontal sur mémoire observée, un
moteur de policy natif en CEL, un coffre-fort de secrets à rotation de clé automatique, une couche de
résilience réseau client (retries + circuit breaker, « mesh sans sidecar »), un reconciler GitOps réel
(sync depuis un dépôt Git), un registre d'images natif, un runtime WebAssembly embarqué (sans conteneur
OCI), un ordonnancement conscient des accélérateurs (GPU/TPU en ressource nommée), une fédération
multi-cluster (fan-out gRPC tolérant aux pannes partielles), une sauvegarde/restauration de cluster, et
— nouveau en Phase 9 — un autoscaler horizontal désormais **conscient de la capacité du cluster**
(il ne monte plus en réplicas au-delà de ce que les nœuds peuvent réellement accueillir) et un moteur
d'estimation de coût FinOps exposé via gRPC.

## Architecture

```
        CONTROL PLANE — une ou plusieurs réplicas (cmd/nimbus-apiserver)
   ┌────────────────────────────────────────────────────────────────────┐
   │  gRPC (mTLS SPIFFE) + AuthInterceptor (RBAC) + otelgrpc (traces)   │
   │  ├─ IdentityService / AdminService                                 │
   │  ├─ PodService / DeploymentService ──► Admission Chain             │
   │  │  ├─ SecurityContextPolicy / ImageSignaturePolicy / Quota        │
   │  │  └─ PolicyValidator ──► internal/policy (moteur CEL natif)      │
   │  ├─ NodeService                                                    │
   │  ├─ VolumeService ──► driver CSI hostpath (Controller/Node/Identity)│
   │  ├─ NetworkPolicyService ──► internal/netpolicy (moteur de policy) │
   │  ├─ PolicyService / SecretService (coffre-fort chiffré au repos)   │
   │  ├─ ImageRegistryService (registre d'images natif, signatures)     │
   │  ├─ FederationService ──► internal/federation (fan-out multi-cluster)│
   │  ├─ BackupService (snapshot/restore de tout le Store)              │
   │  └─ FinOpsService ──► internal/finops (estimation de coût)         │
   │                                                                    │
   │  Controller Manager (actif sur le leader Raft uniquement)         │
   │  ├─ DeploymentReconciler   (désiré vs. observé + scheduler +       │
   │  │    éviction nœud mort/pod en échec terminal, accélérateurs)    │
   │  ├─ NodeHealthReconciler   (heartbeat expiré → not-ready)          │
   │  ├─ HorizontalAutoscaler   (mémoire observée, plafonné par la      │
   │  │    capacité cluster réellement disponible — Phase 9)           │
   │  ├─ KeyRotationReconciler  (fait tourner la clé AES-256-GCM,       │
   │  │    re-chiffre tout, sans interruption de service)              │
   │  └─ gitops.Reconciler      (sync périodique depuis un dépôt Git,   │
   │       applique les Deployment manifests trouvés — Phase 7)        │
   │              │                                                    │
   │              └──────────► RaftStore (BoltDB, chiffré AES-256-GCM) │
   └────────────────────────────────────────────────────────────────────┘
              ▲ Raft (réplication)          ▲ gRPC mTLS (enrôlement, heartbeat,
              │  interceptor mesh (retry +  │  UpdatePodStatus)
              │  circuit breaker, client)   │
     autre réplica control-plane       Nœud (cmd/nimbus-agent)
     ▲                                 enrôle → SVID, s'enregistre comme Node,
     │ gRPC mTLS (identité SPIFFE          heartbeat périodique, boucle de
     │ réutilisée pour l'auth              supervision : exécute chaque
     │ inter-cluster)                      conteneur comme process OS réel
     │                                     (ou module WASM via wazero si
  autre cluster NimbusCore                 `wasm_module_path` est renseigné),
  (fédéré via FederationService)           détecte les sorties, redémarre
                                            selon RestartPolicy, rapporte
                                            phase + mémoire réelle (RSS)
```

- **`internal/store`** — `Store` (Get/Put/Delete/List). `RaftStore` répliqué, chiffré au repos, avec
  rotation de clé (`RotateEncryptionKey` + `ReencryptAll`) : la clé précédente reste utilisable en
  lecture le temps que chaque valeur soit réécrite sous la nouvelle, puis est abandonnée — aucune
  interruption de service pendant la rotation. Le coffre-fort de secrets (`Secret`/`SecretService`)
  hérite directement de ce chiffrement, sans code dédié.
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
- **`internal/policy`** — moteur de policy natif en CEL (Common Expression Language — le même langage
  que Kubernetes utilise depuis sa propre `ValidatingAdmissionPolicy`, à la place de Rego/OPA) :
  compile une expression (`"team" in pod.labels`, `pod.containers.all(c,
  c.image.startsWith("registry.interne/"))`...) et l'évalue contre le pod admis. Branché dans
  l'admission chain via `admission.PolicyValidator` — mêmes primitives que le reste de l'admission
  control, pas un service séparé.
- **`internal/registry`** — accès typé et générique au-dessus du `Store`.
- **`internal/apiserver`** — serveur gRPC, `AuthInterceptor`, tous les services de ressources.
- **`internal/controller`** — `Manager`/`Reconciler`, réconciliateurs (`DeploymentReconciler`,
  `NodeHealthReconciler`, `HorizontalAutoscaler`, `KeyRotationReconciler`), `RunWhileLeader`. Le
  `DeploymentReconciler` planifie sur des métriques d'usage réelles (somme des requêtes CPU/mémoire/
  accélérateurs déjà attribuées par nœud, pas des valeurs figées) et remplace tout pod en phase
  terminale (`Succeeded`/`Failed`), en plus d'évincer ceux d'un nœud mort. Depuis la Phase 9,
  `HorizontalAutoscaler` calcule la capacité cluster restante (`capacity.go`) avant toute décision de
  scale-up et plafonne le nombre de réplicas ajoutées à ce que les nœuds prêts peuvent réellement
  accueillir (CPU, mémoire, accélérateurs), en journalisant le manque à combler plutôt que d'échouer
  silencieusement.
- **`internal/scheduler`** — `Scheduler` filter-then-score, avec filtrage sur ressources nommées
  (`accelerators`, ex. `nvidia.com/gpu`) en plus de CPU/mémoire — mêmes primitives que les « extended
  resources » de Kubernetes.
- **`internal/agent`** — agent de nœud : boucle de réconciliation réelle (liste les pods qui lui sont
  assignés, démarre/arrête de vrais processus OS via `os/exec`, détecte les sorties, redémarre selon
  `RestartPolicy`, échantillonne la mémoire résidente réelle via `gopsutil`, rapporte tout via
  `PodService.UpdatePodStatus`). Si `Container.wasm_module_path` est renseigné, le conteneur est
  exécuté comme module WebAssembly via `internal/wasmrt` (wazero, WASI, zéro cgo) plutôt que comme
  process natif — utile pour des charges légères de type edge sans binaire natif par plateforme. Ce
  n'est pas encore un vrai runtime OCI/CRI (pas d'image, pas d'isolation cgroups/namespaces) —
  `Container.command` exécute directement un binaire côté hôte ; voir les limites connues plus bas.
- **`internal/mesh`** — `CircuitBreaker` (Closed/Open/HalfOpen) et `RetryPolicy` (backoff exponentiel),
  assemblés en un `grpc.UnaryClientInterceptor` réutilisable. « Mesh sans sidecar » : la résilience
  réseau (retries, coupe-circuit) vit dans le processus client, pas dans un proxy injecté à côté.
- **`internal/gitops`** — `Reconciler` qui clone/pull un dépôt Git réel (`go-git`, pur Go, aucun binaire
  `git` requis) à intervalle régulier, décode chaque manifeste `*.json` en `Deployment`
  (`protojson.Unmarshal`) et l'applique dans le registre — le dépôt Git devient la source de vérité.
- **`internal/imageregistry`** — registre d'images natif au cluster (`ImageRecord`/
  `ImageRegistryService`) ; son `Verifier` implémente `admission.ImageVerifier` en vérifiant la
  présence de l'image dans ce registre plutôt qu'en re-validant la signature cryptographique
  (`imagesign` s'en charge déjà à la signature/au push).
- **`internal/federation`** — `Registry` qui fait du fan-out gRPC concurrent vers des clusters distants
  enregistrés (`ListPodsAll`), tolère les pannes partielles (un cluster down ne bloque pas les autres),
  réutilise l'enrôlement SPIFFE existant pour l'authentification inter-cluster.
- **`internal/finops`** — `Estimate(pods, model, labelKey, now)` : coût déterministe ($/cœur-heure,
  $/Go-heure, $/accélérateur-heure) appliqué aux requêtes de ressources de chaque pod × temps
  d'exécution écoulé (`ObjectMeta.created_at_unix`), groupé par namespace et par une clé de label
  arbitraire. Exposé via `FinOpsService.GetCostReport`.
- **`api/v1`** — schéma Protobuf (`Pod`, `Node`, `Deployment`, `Volume`, `NetworkPolicy`, `ImageRecord`,
  `RemoteCluster`, `BackupData`, `CostReport`, services d'identité/admin) et code généré.

## Démarrer

Prérequis : Go 1.25+, `protoc` (uniquement pour régénérer le schéma).

### 0. Installer les binaires (Linux/macOS)

```bash
./install.sh
```

Compile `nimbusctl`, `nimbus-apiserver` et `nimbus-agent`, puis les installe dans `/usr/local/bin`
(`PREFIX=~/.local/bin ./install.sh` pour changer la destination). Sous Windows, utiliser `go build
-o <nom>.exe ./cmd/<binaire>` directement.

**Paquet `.deb` + services systemd** (Debian/Ubuntu) — pour une installation façon paquet système
plutôt qu'un binaire copié à la main, avec `nimbus-apiserver`/`nimbus-agent` gérés par systemd
(démarrage auto, `systemctl status`, logs via `journalctl`) :

```bash
./packaging/build-deb.sh          # produit nimbuscore_0.1.0_amd64.deb à la racine du repo
sudo dpkg -i nimbuscore_0.1.0_amd64.deb
```

Le paquet installe les 3 binaires dans `/usr/bin`, un utilisateur système dédié `nimbuscore` (sans
shell, sans home), les unités `nimbus-apiserver.service`/`nimbus-agent.service`, et des fichiers de
config éditables dans `/etc/nimbuscore/{apiserver,agent}.env` (déclarés `conffiles` — un
`apt upgrade` ultérieur ne les écrasera pas silencieusement). Après installation :

```bash
sudo nano /etc/nimbuscore/apiserver.env   # join-token, etc. — le défaut bootstrap un cluster de test
sudo nano /etc/nimbuscore/agent.env       # node-name, control-plane-addr, join-token
sudo systemctl enable --now nimbus-apiserver
sudo systemctl enable --now nimbus-agent
journalctl -u nimbus-apiserver -f
```

Un `.deb` pré-construit est aussi publié sur les
[releases GitHub](https://github.com/gh0st-nemesis/nimbuscore/releases) — pas besoin de Go ni de
`dpkg-deb` sur la machine cible dans ce cas :

```bash
curl -LO https://github.com/gh0st-nemesis/nimbuscore/releases/latest/download/nimbuscore_0.1.1_amd64.deb
sudo dpkg -i nimbuscore_0.1.1_amd64.deb
```

`build-deb.sh` lui-même cross-compile (`GOOS=linux`) et assemble l'archive `ar`/`tar.gz` à la main —
il tourne aussi bien depuis Windows/macOS que depuis Linux, sans dépendre de `dpkg-deb`.

> **Limites connues** : les deux services tournent sous l'utilisateur système `nimbuscore` avec
> `NoNewPrivileges=true` — cohérent avec le fait que l'agent exécute déjà les conteneurs comme de
> simples processus OS (pas d'isolation cgroups/namespaces, cf. plus bas) : un `Container.command`
> qui exigerait des privilèges élevés à l'intérieur du « conteneur » échouera sous ce service, tout
> comme il échouerait déjà sans ce paquet. Le fichier `apiserver.env` par défaut bootstrap un cluster
> mono-réplica avec vérification de signature d'image désactivée — à durcir avant tout usage au-delà
> du test local (voir la section RBAC/signature plus bas). L'identité de bootstrap (clé privée de la
> CA + clé de chiffrement AES) est persistée dans `<data-dir>/bootstrap-identity.json` (`0600`,
> lisible seulement par l'utilisateur `nimbuscore`) dès le premier démarrage, précisément pour qu'un
> redémarrage du service (`systemctl restart`, reboot, crash) réutilise la même identité au lieu d'en
> régénérer une nouvelle en mémoire — un ancien bug de cette implémentation faisait que chaque
> redémarrage cassait le déchiffrement de tout ce qui avait été écrit par le process précédent.

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

### 5. Piloter le cluster avec nimbusctl (comme kubectl)

`nimbusctl` s'enrôle comme identité `CLIENT` (SPIFFE/mTLS) à chaque invocation — il a donc besoin de
l'adresse du control plane et du join token. Trois façons de les fournir, par ordre de priorité :
un flag explicite (`--control-plane-addr`/`--join-token`), une variable d'environnement, puis un
fichier de config persistant façon `~/.kube/config` :

```bash
# une seule fois, plutôt que d'exporter les variables à chaque session shell
nimbusctl config set-context --control-plane-addr=127.0.0.1:7443 --join-token=devtoken
nimbusctl config view
```

Ou, comme avant, via l'environnement (utile en CI/scripts) :

```bash
export NIMBUS_API_ADDR=127.0.0.1:7443
export NIMBUS_JOIN_TOKEN=devtoken
```

Le fichier est écrit dans `~/.nimbus/config.json` (mode `0600`, JSON en clair — le join token
partagé n'est pas un secret long terme, seulement ce qu'il faut pour obtenir un SVID court terme ;
ne pas y stocker un join token de production sans en avoir conscience).

Créer un Pod ou un Deployment sans écrire de manifeste, façon `kubectl run` :

```bash
nimbusctl run web --image=nginx:v1 -- sleep 3600     # un seul Pod
nimbusctl run api --image=alpine:v1 --replicas=3 -- sleep 3600   # un Deployment à 3 réplicas
```

> Sans `-- <commande>`, `nimbusctl run` prévient : l'agent exécute les conteneurs comme de vrais
> processus OS (pas encore de runtime CRI/OCI, cf. limites connues plus bas), donc sans commande il
> n'y a rien à exécuter — `--image` sert à la vérification de signature, pas à un vrai pull d'image.

Ou avec un manifeste JSON (`kind: Pod|Deployment|Node`), façon `kubectl apply -f` :

```bash
nimbusctl apply -f deployment.json
```

Lister les ressources, façon `kubectl get` :

```bash
nimbusctl get pods -n default
nimbusctl get deployments
nimbusctl get nodes
```

### Self-healing et autoscaling (Phase 5)

Un `Container.command` (ex. `["powershell.exe", "-Command", "Start-Sleep -Seconds 60"]` sur Windows,
`["sleep", "60"]` sur Linux/macOS) est exécuté tel quel par l'agent comme process OS. Avec
`RestartPolicy: RESTART_POLICY_ALWAYS`, un process qui crashe est automatiquement relancé —
`status.restart_count` grimpe à chaque tentative. Un `Deployment` avec `min_replicas`/`max_replicas`
renseignés est piloté par l'`HorizontalAutoscaler` : le nombre de réplicas est ajusté toutes les 15s
sur la mémoire résidente réellement mesurée (`status.memory_usage_bytes`) rapportée à la mémoire
demandée par le conteneur — mêmes formules que l'algorithme HPA de Kubernetes (cible 100% d'utilisation
par défaut).

### Policy native et secrets (Phase 6)

Créer une `Policy` cluster-wide bloque tout `Pod`/`Deployment` qui ne satisfait pas son expression CEL,
au même titre que les autres validateurs d'admission :

```bash
# via un client gRPC — nimbusctl n'a pas encore de commande dédiée
# Policy.Spec.Expression: `"team" in pod.labels`
```

Les `Secret` sont chiffrés au repos comme toute autre ressource (Phase 3). `-secret-key-rotation-interval`
(défaut 24h) contrôle la fréquence à laquelle le control plane régénère la clé, re-chiffre chaque
valeur stockée, puis abandonne l'ancienne clé — testé avec un intervalle de quelques secondes : le
secret reste lisible et intact à travers plusieurs rotations consécutives.

### Mesh, GitOps et registre d'images (Phase 7)

Le control plane peut se synchroniser depuis un dépôt Git contenant des manifestes `Deployment` en
JSON (`protojson`) :

```bash
go run ./cmd/nimbus-apiserver ... \
  -gitops-repo-url=file:///C:/path/vers/mon-repo.git -gitops-branch=main \
  -gitops-path=deployments -gitops-sync-interval=30s
```

Tout appel client (ex. enrôlement inter-cluster, section suivante) peut être enveloppé par
`mesh.NewClientInterceptor(mesh.DefaultRetryPolicy(), breaker)` pour obtenir retries + circuit breaker
sans proxy sidecar. `ImageRegistryService` (`PushImage`/`GetImage`/`ListImages`/`DeleteImage`) n'est
actif que si `-image-signing-pubkey` est renseigné.

### Multi-cluster, edge et charges avancées (Phase 8)

Un cluster peut fédérer un autre cluster NimbusCore via `FederationService.RegisterCluster` (ré-utilise
l'enrôlement SPIFFE pour l'auth) puis interroger tous les clusters fédérés d'un coup avec
`ListFederatedPods` — les résultats partiels sont retournés même si un cluster est injoignable. Un
`Container.wasm_module_path` fait exécuter ce conteneur comme module WebAssembly (wazero/WASI) plutôt
que comme process natif — pratique pour des charges edge sans binaire par plateforme. Un
`ResourceList.accelerators` (ex. `{"nvidia.com/gpu": 1}`) sur les requêtes/limites d'un conteneur est
pris en compte par le scheduler et par le calcul d'usage par nœud, exactement comme CPU/mémoire.
`BackupService.CreateBackup`/`RestoreBackup` sauvegardent/restaurent tout le contenu du `Store`.

### Autoscaling unifié et FinOps (Phase 9)

`HorizontalAutoscaler` ne se contente plus de viser un pourcentage d'utilisation mémoire : avant
d'écrire le nombre de réplicas désiré, il additionne la capacité allouable de tous les nœuds `Ready`
du cluster, soustrait ce qui est déjà consommé, et plafonne la montée en charge à ce qui tient
réellement — CPU, mémoire et accélérateurs confondus. Le manque à combler éventuel est journalisé
(`wanted N replicas but cluster capacity only fits M`).

`FinOpsService.GetCostReport(namespace, label_key)` retourne un coût total ainsi qu'une ventilation par
namespace et par valeur d'un label arbitraire (ex. `team`), à partir d'un modèle de coût configurable
au démarrage (`-cost-cpu-core-hour`, `-cost-memory-gb-hour`, défauts $0.03/cœur-heure et
$0.004/Go-heure) appliqué au temps d'exécution réel de chaque pod.

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
| `CLIENT` | `/client/<name>` | `pods:*`, `deployments:*`, `volumes:*`, `networkpolicies:*`, `policies:*`, `secrets:*`, `images:*`, `backup:*`, `federation:*`, `finops:*`, `nodes:get,list` |
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
> - La rotation de clé de chiffrement est un mécanisme par processus : sur un cluster à plusieurs
>   réplicas control-plane, la nouvelle clé n'est aujourd'hui redistribuée qu'au réplica qui déclenche
>   la rotation (le leader). Un vrai déploiement multi-réplicas aurait besoin de repousser la nouvelle
>   clé aux autres réplicas (même mécanisme que la distribution de la clé initiale à l'enrôlement) avant
>   de lancer `ReencryptAll` — travail futur, cohérent avec la limite déjà notée sur la CA à réplica
>   unique.
> - Le moteur de policy CEL évalue namespace/labels/conteneurs du pod admis ; il n'a pas encore accès
>   aux autres ressources du cluster (quotas déjà consommés, autres pods du namespace...) — les
>   politiques inter-ressources restent du ressort de `QuotaPolicy` pour l'instant.
> - `internal/mesh` est une bibliothèque cliente (interceptor gRPC), pas un service mesh à sidecar :
>   pas de proxy injecté, pas de mTLS transparent au niveau du réseau au-delà de ce que fournit déjà
>   `internal/identity`, pas de tableau de bord de trafic. C'est délibérément le modèle « logique dans
>   le process » plutôt que « logique dans un sidecar ».
> - `internal/gitops` applique des manifestes `Deployment` en JSON via `protojson` — pas de support
>   YAML, pas de Kustomize/Helm, pas de détection de dérive (drift) autre que le prochain sync
>   périodique, pas d'authentification Git au-delà de ce que l'URL du dépôt encode déjà (ex. un jeton
>   dans une URL HTTPS) — pas de gestion dédiée des clés SSH.
> - `internal/imageregistry`/`ImageRegistryService` est un registre natif au cluster (métadonnées +
>   signature), pas un vrai registre OCI Distribution Spec — il ne stocke pas les blobs/couches
>   d'image, seulement une référence et sa signature. Implémenter l'API Distribution complète était
>   disproportionné par rapport à ce que l'admission control exige réellement (savoir qu'une image est
>   connue et signée).
> - Le runtime WASM (`internal/wasmrt`) exécute de vrais modules `.wasm` avec support WASI, mais les
>   modules utilisés dans les tests sont du bytecode WASM écrit à la main (aucune chaîne d'outils
>   `wat2wasm`/TinyGo n'est disponible hors-ligne dans cet environnement) — le runtime lui-même n'est
>   pas simulé, seule la façon dont les modules de test ont été produits est artisanale.
> - L'ordonnancement conscient des accélérateurs (`ResourceList.accelerators`) suit le même modèle que
>   les « extended resources » de Kubernetes : une carte nom→quantité comptée par le scheduler et le
>   calcul d'usage par nœud. Il n'y a pas de découverte matérielle réelle (pas d'équivalent
>   device-plugin NVIDIA) — la capacité d'un nœud en accélérateurs est déclarée manuellement dans son
>   `NodeStatus.Allocatable`, pas détectée.
> - La fédération multi-cluster (`internal/federation`) fait du fan-out en lecture
>   (`ListFederatedPods`) et de l'enregistrement de clusters distants ; il n'y a pas encore de
>   routage intelligent par zone/latence, ni de réplication d'écriture cross-cluster — chaque cluster
>   fédéré reste souverain sur ses propres ressources.
> - `BackupService` produit un instantané complet du `Store` (toutes les clés, à un instant donné) et
>   le restaure tel quel ; pas de sauvegarde incrémentale, pas de streaming pour de très gros clusters,
>   pas de chiffrement additionnel du dump au-delà du chiffrement au repos déjà appliqué aux valeurs
>   individuelles.
> - Le plafonnement de capacité de l'`HorizontalAutoscaler` (Phase 9) ne compte que l'usage des pods
>   déjà assignés à un nœud (`Spec.node_name` renseigné) — un pic soudain de pods en attente
>   d'ordonnancement n'est pas anticipé tant qu'ils ne sont pas passés par le scheduler au moins une
>   fois. Il ne provisionne pas non plus de nouveaux nœuds (pas de cluster-autoscaler façon cloud) :
>   il se contente de ne pas promettre plus de réplicas que les nœuds existants ne peuvent accueillir.
> - `internal/finops` est un modèle de coût déterministe et volontairement simple ($/ressource/heure
>   constant, pas de tarification spot/réservée, pas d'ingestion de factures cloud réelles) — un ordre
>   de grandeur utile pour comparer des namespaces/équipes entre eux, pas un remplacement d'un outil de
>   facturation cloud.

Lancer les tests (Raft mono-nœud réel, chiffrement au repos vérifié sur le fichier BoltDB, rotation de
clé vérifiée sur le ciphertext brut, handshake mTLS réel, admission control + RBAC + policies CEL
testés bout-en-bout par gRPC, spans OpenTelemetry vérifiés en mémoire, driver CSI qui crée/liste/supprime
de vrais répertoires) :

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
| `google/cel-go` | Moteur de policy natif (CEL — même langage que Kubernetes `ValidatingAdmissionPolicy`) |
| `shirou/gopsutil` | Mémoire résidente réelle des processus supervisés par l'agent |
| `go-git/v5` | Reconciler GitOps — clone/pull de dépôts Git réels, pur Go (pas de binaire `git`) |
| `tetratelabs/wazero` | Runtime WebAssembly embarqué (WASI), zéro cgo — exécution de conteneurs edge |
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
- [x] **Phase 6 — Gouvernance et observabilité natives** : moteur de policy interne en CEL (mêmes
      primitives que l'admission control), coffre-fort de secrets chiffré au repos avec rotation de
      clé automatique et sans interruption (télémétrie native déjà couverte en Phase 4).
- [x] **Phase 7 — Mesh et GitOps natifs** : résilience réseau côté client (retries + circuit breaker,
      « mesh sans sidecar »), reconciler GitOps réel synchronisant des `Deployment` depuis un dépôt Git
      (`go-git`), registre d'images natif au cluster (`ImageRegistryService`).
- [x] **Phase 8 — Multi-cluster, edge et charges avancées** : fédération multi-cluster tolérante aux
      pannes partielles (`FederationService`, fan-out gRPC), runtime WebAssembly embarqué pour les
      charges edge (`wazero`), ordonnancement conscient des accélérateurs (GPU/TPU en ressource
      nommée), sauvegarde/restauration complète du cluster (`BackupService`).
- [x] **Phase 9 — Autoscaling unifié et FinOps** : `HorizontalAutoscaler` désormais plafonné par la
      capacité cluster réellement disponible (CPU/mémoire/accélérateurs, pas seulement le taux
      d'utilisation mémoire), moteur d'estimation de coût FinOps exposé via `FinOpsService`
      (ventilation par namespace et par label).
