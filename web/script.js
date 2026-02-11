(() => {
  const $ = (id) => document.getElementById(id);

  const cssStatus = $("cssStatus");
  const jsStatus  = $("jsStatus");
  const nowEl     = $("now");
  const counterEl = $("counter");
  const incBtn    = $("incBtn");
  const pingBtn   = $("pingBtn");
  const pingOut   = $("pingOut");

  const card = document.querySelector(".card");
  const cardBg = getComputedStyle(card).backgroundColor;
  cssStatus.textContent = "загружен";
  cssStatus.classList.add("ok");

  jsStatus.textContent = "загружен";
  jsStatus.classList.add("ok");

  const tick = () => {
    nowEl.textContent = new Date().toISOString();
  };
  tick();
  setInterval(tick, 1000);

  let counter = 0;
  incBtn.addEventListener("click", () => {
    counter += 1;
    counterEl.textContent = String(counter);
  });

  pingBtn.addEventListener("click", async () => {
    pingOut.textContent = "Запрос...";
    pingOut.classList.add("muted");

    try {
      const res = await fetch("./health", { cache: "no-store" });
      const text = await res.text();

      pingOut.textContent = `HTTP ${res.status}: ${text.slice(0, 120)}`;
      pingOut.classList.remove("muted");
    } catch (e) {
      pingOut.textContent = `Ошибка: ${e?.message ?? e}`;
      pingOut.classList.add("muted");
    }
  });
})();
