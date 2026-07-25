document.getElementById("login-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const errorEl = document.getElementById("login-error");
  const submitBtn = document.getElementById("login-submit-btn");
  const card = document.querySelector(".login-card");
  errorEl.textContent = "";

  const fd = new FormData(ev.target);
  const username = String(fd.get("username") || "");
  const password = String(fd.get("password") || "");

  submitBtn.disabled = true;
  const originalLabel = submitBtn.textContent;
  submitBtn.textContent = "Signing in…";

  try {
    const resp = await fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    if (resp.ok) {
      window.location.href = "/";
      return;
    }
    errorEl.textContent = resp.status === 401 ? "Invalid username or password" : "Sign-in failed, please try again";
    card.classList.remove("shake");
    void card.offsetWidth;
    card.classList.add("shake");
  } catch (err) {
    errorEl.textContent = "Network error: " + (err.message || String(err));
  } finally {
    submitBtn.disabled = false;
    submitBtn.textContent = originalLabel;
  }
});
