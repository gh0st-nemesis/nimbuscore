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
  <img alt="containerd" src="https://img.shields.io/badge/requires-containerd-575757?logo=containerd&logoColor=white">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-blue.svg"></a>
</p>

---

## Installer

Un `.deb` autonome (aucun besoin de Go ni de git sur la machine cible) :

```bash
curl -LO https://github.com/gh0st-nemesis/nimbuscore/releases/latest/download/nimbuscore_0.1.0_amd64.deb
sudo dpkg -i nimbuscore_0.1.0_amd64.deb
```

L'installation **demande le rôle de la machine** (`master` ou `worker`) et n'active que le service
correspondant — ça évite d'activer un agent sur le control plane ou l'inverse par erreur. Choisir
`worker` installe aussi containerd automatiquement si absent (`apt install containerd`). Sans
terminal interactif (script, CI, install à distance non-interactive), aucun rôle n'est présumé :
aucun service n'est activé tant que tu n'as pas relancé `sudo dpkg-reconfigure nimbuscore` ou activé
le bon toi-même.

> `nimbus-agent` tourne en root sur les nœuds `worker` (comme un vrai kubelet) : il pilote
> containerd directement via son client Go officiel (pas de binaire `ctr` shell-outé), et
> démarrer un conteneur implique des opérations de montage overlay qui demandent
> `CAP_SYS_ADMIN`+`CAP_DAC_OVERRIDE` — en pratique proche de root de toute façon.
> `nimbus-apiserver`, lui, tourne sous un utilisateur système dédié non-root (`nimbuscore`).

## Démarrer un cluster

Sur la machine qui sera le control plane (rôle `master` choisi à l'install) :

```bash
sudo nano /etc/nimbuscore/apiserver.env   # remplacer les CHANGEME (join-token, mot de passe dashboard)
sudo systemctl start nimbus-apiserver
```

Un dashboard web natif est servi sur `:8080` (`http://<ip-du-control-plane>:8080/`, identifiant
`admin` par défaut) — nodes, pods, deployments, services et coûts FinOps, rafraîchi toutes les 5s.
Sans `-dashboard-password`, il sert sans authentification (log d'avertissement) ; à ne faire que sur
un réseau de confiance.

Sur chaque machine qui exécutera des pods (rôle `worker` choisi à l'install) :

```bash
sudo nano /etc/nimbuscore/agent.env       # node-name, control-plane-addr, même join-token
sudo systemctl start nimbus-agent
```

Configurer le client :

```bash
nimbusctl config set-context --control-plane-addr=<ip-du-control-plane>:7443 --join-token=<ton-token>
nimbusctl get nodes
```

## Déployer depuis un repo Git

Le dashboard peut construire et déployer directement depuis un repo Git — **pas besoin de
Dockerfile** : si le repo n'en a pas, la techno est détectée automatiquement (`package.json` →
Node.js, sinon `index.html` → site statique servi par nginx). Pour les apps Node.js qui déclarent un
script `"build"` (Next.js, Vite, Create React App, ...), ce build est exécuté avant le démarrage —
avec les devDependencies installées le temps du build, puis retirées de l'image finale. Les autres
stacks échouent avec un message clair plutôt que de déployer silencieusement un conteneur cassé.

### Connexion GitHub

Pour éviter de coller l'URL du repo à chaque déploiement, une connexion **GitHub OAuth** optionnelle
permet de choisir le repo (public ou privé) dans une liste, directement dans le formulaire de
déploiement :

1. Créer une OAuth App sur `github.com/settings/developers`, avec pour callback URL
   `http://<ip-du-control-plane>:8080/auth/github/callback`.
2. Ajouter les identifiants à `/etc/nimbuscore/apiserver.env`, à la suite de
   `NIMBUS_APISERVER_ARGS` :
   ```
   -github-oauth-client-id=<client-id> -github-oauth-client-secret=<client-secret>
   ```
3. `sudo systemctl restart nimbus-apiserver`, puis bouton **Connect GitHub** dans le formulaire
   « Deploy from Git repo ».

Une fois connecté, le token GitHub stocké sert aussi à cloner automatiquement les repos privés
choisis dans la liste (pas besoin de coller un token à la main). Sans cette configuration, la
fonctionnalité reste simplement invisible et l'URL du repo se saisit à la main (comportement par
défaut, aucune app GitHub requise) — les repos publics se clonent alors sans authentification.

## Stockage persistant

Un déploiement peut monter un volume persistant (namespace + nom réutilisés comme identifiant), à
cocher dans le formulaire de déploiement du dashboard avec un chemin de montage (ex.
`/usr/share/nginx/html`). Les déploiements avec stockage persistant sont limités à **1 réplica**
(le volume vit sur un seul nœud). Le contenu est éditable directement depuis l'onglet **Files** du
dashboard (lecture/écriture/suppression de fichiers), sans redéployer le conteneur.

> Éditer un déploiement existant (image, commande, variables d'env, repo Git...) depuis le panneau
> du dashboard préserve automatiquement son stockage persistant s'il en a un — le formulaire
> d'édition n'a pas de case à cocher dédiée, donc omettre ce champ ne doit jamais être compris comme
> « retire le volume ».

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
