(function () {
  const reduceMotion = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  const canvas = document.createElement("canvas");
  canvas.className = "particles-canvas";
  document.body.prepend(canvas);
  const ctx = canvas.getContext("2d");

  const accentRGB = getComputedStyle(document.documentElement).getPropertyValue("--accent-rgb").trim() || "124, 58, 237";
  const accent2RGB = "96, 165, 250"; // --blue-2, used for the cursor glow so it reads distinct from the particle network itself

  const COUNT = 130;
  const LINK_DIST = 170;
  const MOUSE_RADIUS = 200;
  const MOUSE_LINK_DIST = 230;
  const SPEED = 0.35;

  let width = 0;
  let height = 0;
  let particles = [];
  const mouse = { x: -9999, y: -9999, active: false };

  function resize() {
    width = canvas.width = window.innerWidth;
    height = canvas.height = window.innerHeight;
  }

  function makeParticles() {
    particles = [];
    for (let i = 0; i < COUNT; i++) {
      const big = Math.random() < 0.15;
      particles.push({
        x: Math.random() * width,
        y: Math.random() * height,
        vx: (Math.random() - 0.5) * SPEED,
        vy: (Math.random() - 0.5) * SPEED,
        r: big ? 2.2 + Math.random() * 1.4 : 1 + Math.random() * 0.8,
        glow: big,
      });
    }
  }

  function step() {
    ctx.clearRect(0, 0, width, height);

    // Repel particles gently away from the cursor — this is what makes the
    // field feel alive rather than a static wallpaper.
    if (mouse.active) {
      for (const p of particles) {
        const dx = p.x - mouse.x;
        const dy = p.y - mouse.y;
        const dist = Math.sqrt(dx * dx + dy * dy);
        if (dist < MOUSE_RADIUS && dist > 0.01) {
          const force = (1 - dist / MOUSE_RADIUS) * 0.6;
          p.vx += (dx / dist) * force;
          p.vy += (dy / dist) * force;
        }
      }
    }

    for (const p of particles) {
      p.x += p.vx;
      p.y += p.vy;
      // gentle drag so the repel force doesn't accumulate into chaos
      p.vx *= 0.98;
      p.vy *= 0.98;
      const minSpeed = 0.04;
      if (Math.abs(p.vx) < minSpeed) p.vx += (Math.random() - 0.5) * 0.05;
      if (Math.abs(p.vy) < minSpeed) p.vy += (Math.random() - 0.5) * 0.05;
      if (p.x < 0 || p.x > width) p.vx *= -1;
      if (p.y < 0 || p.y > height) p.vy *= -1;
      p.x = Math.max(0, Math.min(width, p.x));
      p.y = Math.max(0, Math.min(height, p.y));
    }

    // particle-to-particle connections
    for (let i = 0; i < particles.length; i++) {
      for (let j = i + 1; j < particles.length; j++) {
        const a = particles[i];
        const b = particles[j];
        const dx = a.x - b.x;
        const dy = a.y - b.y;
        const dist = Math.sqrt(dx * dx + dy * dy);
        if (dist < LINK_DIST) {
          ctx.beginPath();
          ctx.moveTo(a.x, a.y);
          ctx.lineTo(b.x, b.y);
          ctx.strokeStyle = `rgba(${accentRGB}, ${0.32 * (1 - dist / LINK_DIST)})`;
          ctx.lineWidth = 1;
          ctx.stroke();
        }
      }
    }

    // connections from nearby particles to the cursor itself
    if (mouse.active) {
      for (const p of particles) {
        const dx = p.x - mouse.x;
        const dy = p.y - mouse.y;
        const dist = Math.sqrt(dx * dx + dy * dy);
        if (dist < MOUSE_LINK_DIST) {
          ctx.beginPath();
          ctx.moveTo(p.x, p.y);
          ctx.lineTo(mouse.x, mouse.y);
          ctx.strokeStyle = `rgba(${accent2RGB}, ${0.4 * (1 - dist / MOUSE_LINK_DIST)})`;
          ctx.lineWidth = 1.2;
          ctx.stroke();
        }
      }
    }

    for (const p of particles) {
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
      ctx.fillStyle = `rgba(${accentRGB}, 0.85)`;
      if (p.glow) {
        ctx.shadowColor = `rgba(${accentRGB}, 0.9)`;
        ctx.shadowBlur = 8;
      } else {
        ctx.shadowBlur = 0;
      }
      ctx.fill();
    }
    ctx.shadowBlur = 0;

    if (mouse.active) {
      ctx.beginPath();
      ctx.arc(mouse.x, mouse.y, 2.6, 0, Math.PI * 2);
      ctx.fillStyle = `rgba(${accent2RGB}, 0.9)`;
      ctx.shadowColor = `rgba(${accent2RGB}, 0.8)`;
      ctx.shadowBlur = 12;
      ctx.fill();
      ctx.shadowBlur = 0;
    }

    if (!reduceMotion) requestAnimationFrame(step);
  }

  resize();
  makeParticles();

  if (reduceMotion) {
    step(); // one static frame, no motion loop, no mouse tracking
    return;
  }

  window.addEventListener("resize", () => {
    resize();
    makeParticles();
  });
  window.addEventListener("mousemove", (ev) => {
    mouse.x = ev.clientX;
    mouse.y = ev.clientY;
    mouse.active = true;
  });
  window.addEventListener("mouseleave", () => {
    mouse.active = false;
  });
  requestAnimationFrame(step);
})();
