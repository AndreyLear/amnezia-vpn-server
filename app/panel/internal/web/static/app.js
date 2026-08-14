/* AmneziaVPN Panel — progressive enhancement (T-120 round 2).
 * External-only script (CSP default-src 'self'): no inline handlers,
 * no eval. Everything degrades gracefully: without this file every
 * mutation form still POSTs and the server answers 303 + flash.
 *
 * What it adds:
 *   - toasts (bottom-right, auto-dismiss ~4s) instead of flash reloads;
 *   - fetch()-based mutations (CSRF token from the form) with local DOM
 *     updates (card fragments from the server, badges, counters);
 *   - native <dialog> modals for the add form, QR codes and the delete
 *     confirmation (lazy QR loading on open);
 *   - fallback `open` dialogs are upgraded to real modals.
 */
(() => {
  "use strict";

  const doc = document;
  const root = doc.documentElement;

  // JS is on: hide the no-JS fallback dialogs and enable modal styling.
  root.classList.add("js");
  doc.querySelectorAll("dialog[open]").forEach((dlg) => dlg.close());

  /* ---- toasts ----------------------------------------------------- */

  const toasts = doc.createElement("div");
  toasts.className = "toasts";
  doc.body.appendChild(toasts);

  function showToast(message, kind) {
    const el = doc.createElement("div");
    el.className = "toast " + (kind === "error" ? "error" : "ok");
    el.textContent = message;
    toasts.appendChild(el);
    window.setTimeout(() => {
      el.classList.add("leaving");
      window.setTimeout(() => el.remove(), 300);
    }, 4000);
  }

  /* ---- dialogs ----------------------------------------------------- */

  function openDialog(id) {
    const dlg = doc.getElementById(id);
    if (!dlg) return;
    if (typeof dlg.showModal === "function") dlg.showModal();
    else dlg.setAttribute("open", "");
    const img = dlg.querySelector("img[data-qr-src]");
    if (img && !img.src) img.src = img.getAttribute("data-qr-src");
  }

  function closeDialog(dlg) {
    if (typeof dlg.close === "function") dlg.close();
    else dlg.removeAttribute("open");
  }

  doc.addEventListener("click", (e) => {
    const opener = e.target.closest("[data-dialog-open]");
    if (opener) {
      e.preventDefault();
      openDialog(opener.getAttribute("data-dialog-open"));
      return;
    }
    const closer = e.target.closest("[data-dialog-close]");
    if (closer) {
      const dlg = closer.closest("dialog");
      if (dlg) closeDialog(dlg);
      return;
    }
    // Click on the backdrop closes the dialog.
    if (e.target.tagName === "DIALOG") {
      const rect = e.target.getBoundingClientRect();
      if (
        e.clientX < rect.left || e.clientX > rect.right ||
        e.clientY < rect.top || e.clientY > rect.bottom
      ) {
        closeDialog(e.target);
      }
    }
  });

  /* ---- fetch mutations --------------------------------------------- */

  const cardBox = doc.getElementById("clients");
  const countBadge = doc.getElementById("client-count");
  const countChip = doc.getElementById("clients-chip");

  function updateCount(n) {
    if (countBadge) countBadge.textContent = String(n);
    if (countChip) countChip.textContent = "Клиентов: " + n;
  }

  async function submitMutation(form) {
    const body = new URLSearchParams(new FormData(form));
    let resp;
    try {
      resp = await window.fetch(form.getAttribute("action"), {
        method: (form.getAttribute("method") || "post").toUpperCase(),
        headers: {
          Accept: "application/json",
          "X-Requested-With": "fetch",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: body.toString(),
      });
    } catch (err) {
      return { ok: false, message: "Сетевая ошибка" };
    }
    if (!resp.ok) {
      return { ok: false, message: "Ошибка сервера (" + resp.status + ")" };
    }
    try {
      return await resp.json();
    } catch (err) {
      return { ok: false, message: "Некорректный ответ сервера" };
    }
  }

  doc.addEventListener("submit", async (e) => {
    const form = e.target.closest("form[data-mutate]");
    if (!form) return;
    e.preventDefault();
    const kind = form.getAttribute("data-mutate");
    const clientId = form.getAttribute("data-client-id");
    const submitBtn = form.querySelector('button[type="submit"]');
    if (submitBtn) submitBtn.disabled = true;
    try {
      const data = await submitMutation(form);
      if (!data || !data.ok) {
        showToast((data && data.message) || "Не удалось выполнить действие", "error");
        return;
      }
      showToast(data.message || "Готово", "ok");

      if (kind === "add") {
        if (data.html && cardBox) {
          cardBox.insertAdjacentHTML("beforeend", data.html);
        } else {
          window.location.reload();
          return;
        }
        const dlg = form.closest("dialog");
        if (dlg) closeDialog(dlg);
        form.reset();
      } else if (kind === "delete") {
        if (clientId) {
          const card = doc.getElementById("client-" + clientId);
          if (card) card.remove();
        } else {
          window.location.reload();
          return;
        }
        const dlg = form.closest("dialog");
        if (dlg) closeDialog(dlg);
      } else {
        // toggle/rename/expiry: replace the card with the fresh fragment.
        if (data.html && clientId) {
          const card = doc.getElementById("client-" + clientId);
          if (card) card.outerHTML = data.html;
        } else {
          window.location.reload();
          return;
        }
      }
      if (typeof data.count === "number") updateCount(data.count);
    } finally {
      if (submitBtn) submitBtn.disabled = false;
    }
  });
})();
