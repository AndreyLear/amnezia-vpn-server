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
  toasts.className =
    "fixed right-5 bottom-5 z-[100] flex max-w-[min(360px,calc(100vw_-_40px))] flex-col gap-2.5";
  doc.body.appendChild(toasts);

  function showToast(message, kind) {
    const el = doc.createElement("div");
    el.className =
      "animate-toast-in rounded-btn border px-4 py-3 text-[13.5px] font-medium break-words shadow-lg transition duration-300 " +
      (kind === "error"
        ? "border-error/40 bg-error/10 text-[#ffb3b4]"
        : "border-success/40 bg-success/10 text-[#9ef0c6]");
    el.textContent = message;
    el.setAttribute("role", "status");
    const dismiss = () => {
      el.classList.add("opacity-0", "translate-y-1.5");
      window.setTimeout(() => el.remove(), 300);
    };
    el.addEventListener("click", dismiss);
    toasts.appendChild(el);
    window.setTimeout(dismiss, 4000);
  }

  /* ---- dialogs ----------------------------------------------------- */

  function openDialog(id) {
    const dlg = doc.getElementById(id);
    if (!dlg) return;
    const target = dlg.querySelector("button, input, textarea, select, a");
    if (typeof dlg.showModal === "function") dlg.showModal();
    else dlg.setAttribute("open", "");
    if (target) target.focus();
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

  /* ---- login 2FA modal (keep password in the live form) ------------ */

  const loginForm = doc.getElementById("login-form");
  const login2fa = doc.getElementById("login-2fa");
  const login2faCode = doc.getElementById("login-2fa-code");
  const login2faError = doc.getElementById("login-2fa-error");
  const login2faConfirm = doc.getElementById("login-2fa-confirm");
  const loginWrongPassword = "Неверное имя пользователя или пароль.";
  const loginWrongCode = "Неверный код.";
  const loginNeedCodeLabel = "Код из приложения";

  function loginNeedCodeHTML(html) {
    return html.indexOf('name="code"') !== -1 && html.indexOf(loginNeedCodeLabel) !== -1;
  }

  function loginShowFlash(message) {
    if (!loginForm) return;
    let flash = doc.querySelector(".login-card > .flash.flash-error");
    if (!flash) {
      flash = doc.createElement("p");
      flash.className = "flash flash-error";
      flash.setAttribute("role", "alert");
      loginForm.parentNode.insertBefore(flash, loginForm);
    }
    flash.hidden = false;
    flash.textContent = message;
  }

  function loginSetHiddenCode(form, code) {
    let field = form.querySelector('input[type="hidden"][name="code"]');
    if (!field) {
      field = doc.createElement("input");
      field.type = "hidden";
      field.name = "code";
      form.appendChild(field);
    }
    field.value = code;
  }

  function loginStripHiddenCode(form) {
    const hidden = form.querySelector('input[type="hidden"][name="code"]');
    if (hidden) hidden.remove();
  }

  function loginClear2FA() {
    if (login2faCode) login2faCode.value = "";
    if (login2faError) {
      login2faError.hidden = true;
      login2faError.textContent = "";
    }
    if (loginForm) loginStripHiddenCode(loginForm);
  }

  function loginOpen2FA(errorText) {
    if (login2faError) {
      if (errorText) {
        login2faError.hidden = false;
        login2faError.textContent = errorText;
      } else {
        login2faError.hidden = true;
        login2faError.textContent = "";
      }
    }
    if (login2fa && login2fa.open) {
      if (login2faCode) login2faCode.focus();
      return;
    }
    openDialog("login-2fa");
    if (login2faCode) login2faCode.focus();
  }

  async function loginPost(form) {
    const body = new URLSearchParams(new FormData(form));
    const resp = await window.fetch("/login", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: body.toString(),
      redirect: "follow",
    });
    if (resp.redirected) {
      try {
        const dest = new URL(resp.url, window.location.origin);
        if (dest.pathname === "/") {
          window.location.assign(resp.url);
          return;
        }
      } catch (err) {
        window.location.assign("/");
        return;
      }
    }
    if (resp.status === 429) {
      const text = (await resp.text()).trim();
      if (login2fa && login2fa.open) closeDialog(login2fa);
      loginShowFlash(text || "Слишком много попыток входа. Подождите и попробуйте снова.");
      return;
    }
    if (resp.status !== 200) {
      loginShowFlash("Ошибка сервера (" + resp.status + ")");
      return;
    }
    const html = await resp.text();
    const needCode = loginNeedCodeHTML(html);
    if (needCode) {
      const wrongCode = html.indexOf(loginWrongCode) !== -1;
      loginOpen2FA(wrongCode ? loginWrongCode : "");
      return;
    }
    if (html.indexOf(loginWrongPassword) !== -1) {
      loginShowFlash(loginWrongPassword);
      if (login2fa && login2fa.open) closeDialog(login2fa);
      return;
    }
    loginShowFlash("Некорректный ответ сервера");
  }

  if (loginForm) {
    if (login2fa) {
      login2fa.addEventListener("close", loginClear2FA);
    }
    loginForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const confirming = login2fa && login2fa.open;
      if (confirming && login2faCode) {
        loginSetHiddenCode(loginForm, login2faCode.value);
      } else {
        loginStripHiddenCode(loginForm);
      }
      try {
        await loginPost(loginForm);
      } catch (err) {
        loginShowFlash("Сетевая ошибка");
      }
    });
    const login2faForm = doc.getElementById("login-2fa-form");
    function loginConfirm2FA(e) {
      e.preventDefault();
      if (!login2faCode) return;
      loginSetHiddenCode(loginForm, login2faCode.value);
      if (typeof loginForm.requestSubmit === "function") loginForm.requestSubmit();
      else loginForm.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    }
    if (login2faForm) login2faForm.addEventListener("submit", loginConfirm2FA);
    else if (login2faConfirm) login2faConfirm.addEventListener("click", loginConfirm2FA);
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
        // toggle/rename: replace the card with the fresh fragment.
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
