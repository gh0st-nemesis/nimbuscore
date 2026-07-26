import * as THREE from "./vendor/three.module.min.js";

(function () {
  const reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  function cssColor(varName, fallback) {
    const v = getComputedStyle(document.documentElement).getPropertyValue(varName).trim();
    return v || fallback;
  }
  const accentColor = new THREE.Color(cssColor("--accent", "#7c3aed"));
  const accent2Color = new THREE.Color(cssColor("--blue-2", "#60a5fa"));

  const COUNT = reduceMotion ? 70 : 160;
  const SPREAD_X = 900;
  const SPREAD_Y = 550;
  const SPREAD_Z = 900;
  const LINK_DIST = 150;
  const DRIFT_SPEED = 0.35;
  const AUTO_ROTATE_SPEED = 0.00025;

  const canvas = document.createElement("canvas");
  canvas.className = "particles-canvas";
  document.body.prepend(canvas);

  const renderer = new THREE.WebGLRenderer({ canvas, alpha: true, antialias: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.setSize(window.innerWidth, window.innerHeight);

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(55, window.innerWidth / window.innerHeight, 1, 4000);
  camera.position.z = 620;

  const group = new THREE.Group();
  scene.add(group);

  // --- particle points -------------------------------------------------
  const positions = new Float32Array(COUNT * 3);
  const velocities = new Float32Array(COUNT * 3);
  for (let i = 0; i < COUNT; i++) {
    positions[i * 3] = (Math.random() - 0.5) * SPREAD_X;
    positions[i * 3 + 1] = (Math.random() - 0.5) * SPREAD_Y;
    positions[i * 3 + 2] = (Math.random() - 0.5) * SPREAD_Z;
    velocities[i * 3] = (Math.random() - 0.5) * DRIFT_SPEED;
    velocities[i * 3 + 1] = (Math.random() - 0.5) * DRIFT_SPEED;
    velocities[i * 3 + 2] = (Math.random() - 0.5) * DRIFT_SPEED;
  }
  const pointsGeometry = new THREE.BufferGeometry();
  pointsGeometry.setAttribute("position", new THREE.Float32BufferAttribute(positions, 3));
  const pointsMaterial = new THREE.PointsMaterial({
    color: accentColor,
    size: 5,
    sizeAttenuation: true,
    transparent: true,
    opacity: 0.85,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
  const points = new THREE.Points(pointsGeometry, pointsMaterial);
  group.add(points);

  // --- connecting lines, rebuilt from current positions every frame ----
  const MAX_SEGMENTS = COUNT * 12;
  const linePositions = new Float32Array(MAX_SEGMENTS * 2 * 3);
  const lineGeometry = new THREE.BufferGeometry();
  lineGeometry.setAttribute("position", new THREE.Float32BufferAttribute(linePositions, 3));
  lineGeometry.setDrawRange(0, 0);
  const lineMaterial = new THREE.LineBasicMaterial({
    color: accent2Color,
    transparent: true,
    opacity: 0.22,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
  const lines = new THREE.LineSegments(lineGeometry, lineMaterial);
  group.add(lines);

  function updateLines() {
    let segIndex = 0;
    for (let i = 0; i < COUNT && segIndex < MAX_SEGMENTS; i++) {
      const ix = i * 3;
      for (let j = i + 1; j < COUNT && segIndex < MAX_SEGMENTS; j++) {
        const jx = j * 3;
        const dx = positions[ix] - positions[jx];
        const dy = positions[ix + 1] - positions[jx + 1];
        const dz = positions[ix + 2] - positions[jx + 2];
        const distSq = dx * dx + dy * dy + dz * dz;
        if (distSq < LINK_DIST * LINK_DIST) {
          const base = segIndex * 6;
          linePositions[base] = positions[ix];
          linePositions[base + 1] = positions[ix + 1];
          linePositions[base + 2] = positions[ix + 2];
          linePositions[base + 3] = positions[jx];
          linePositions[base + 4] = positions[jx + 1];
          linePositions[base + 5] = positions[jx + 2];
          segIndex++;
        }
      }
    }
    lineGeometry.setDrawRange(0, segIndex * 2);
    lineGeometry.attributes.position.needsUpdate = true;
  }

  function updateParticles() {
    for (let i = 0; i < COUNT; i++) {
      const ix = i * 3;
      positions[ix] += velocities[ix];
      positions[ix + 1] += velocities[ix + 1];
      positions[ix + 2] += velocities[ix + 2];
      if (positions[ix] < -SPREAD_X / 2 || positions[ix] > SPREAD_X / 2) velocities[ix] *= -1;
      if (positions[ix + 1] < -SPREAD_Y / 2 || positions[ix + 1] > SPREAD_Y / 2) velocities[ix + 1] *= -1;
      if (positions[ix + 2] < -SPREAD_Z / 2 || positions[ix + 2] > SPREAD_Z / 2) velocities[ix + 2] *= -1;
    }
    pointsGeometry.attributes.position.needsUpdate = true;
  }

  // --- mouse parallax ----------------------------------------------------
  const mouse = { x: 0, y: 0 };
  const targetCameraOffset = { x: 0, y: 0 };

  function render() {
    if (!reduceMotion) {
      updateParticles();
      group.rotation.y += AUTO_ROTATE_SPEED;
      group.rotation.x += AUTO_ROTATE_SPEED * 0.3;
      targetCameraOffset.x += (mouse.x * 80 - targetCameraOffset.x) * 0.03;
      targetCameraOffset.y += (-mouse.y * 60 - targetCameraOffset.y) * 0.03;
      camera.position.x = targetCameraOffset.x;
      camera.position.y = targetCameraOffset.y;
      camera.lookAt(0, 0, 0);
    }
    updateLines();
    renderer.render(scene, camera);
    if (!reduceMotion) requestAnimationFrame(render);
  }

  function resize() {
    camera.aspect = window.innerWidth / window.innerHeight;
    camera.updateProjectionMatrix();
    renderer.setSize(window.innerWidth, window.innerHeight);
  }

  render();

  if (reduceMotion) return; // one static frame, no listeners, no loop

  window.addEventListener("resize", resize);
  window.addEventListener("mousemove", (ev) => {
    mouse.x = (ev.clientX / window.innerWidth) * 2 - 1;
    mouse.y = (ev.clientY / window.innerHeight) * 2 - 1;
  });
})();
