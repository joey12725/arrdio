<script setup lang="ts">
const email = ref(""),
  password = ref(""),
  message = ref(""),
  busy = ref(false);
const accounts = ref<{ id: number; email: string; healthy: boolean }[]>([]);
const grants = ref<{ id: string; label: string; revoked: boolean }[]>([]);
const prompt = ref("");
async function load() {
  try {
    [accounts.value, grants.value] = await Promise.all([
      $fetch<typeof accounts.value>("/api/arrdio/accounts"),
      $fetch<typeof grants.value>("/api/arrdio/grants"),
    ]);
  } catch {
    message.value = "Settings are temporarily unavailable";
  }
}
async function add() {
  busy.value = true;
  message.value = "";
  try {
    await $fetch("/api/arrdio/accounts", {
      method: "POST",
      body: { email: email.value, password: password.value },
    });
    email.value = "";
    password.value = "";
    message.value = "Account validated and added to the household pool.";
    await load();
  } catch {
    message.value =
      "Could not validate this account. Check the credentials and try again.";
  } finally {
    busy.value = false;
  }
}
async function pair() {
  try {
    const data = await $fetch<{ prompt: string }>("/api/arrdio/bootstrap", {
      method: "POST",
    });
    prompt.value = data.prompt;
    await navigator.clipboard.writeText(data.prompt);
    message.value =
      "Integration prompt copied. Share it privately with your agent within 15 minutes.";
  } catch {
    message.value = prompt.value
      ? "Copy the integration prompt below."
      : "Could not create a pairing prompt.";
  }
}
async function revoke(id: string) {
  try {
    await $fetch(`/api/arrdio/grants/${id}/revoke`, { method: "POST" });
    await load();
  } catch {
    message.value = "Could not revoke this connection.";
  }
}
onMounted(load);
</script>
<template>
  <h1>Settings</h1>
  <p v-if="message" class="notice" role="status">{{ message }}</p>
  <div class="settings-grid">
    <section class="panel">
      <h2>Ebook accounts</h2>
      <p class="muted">
        Add an account to the household pool. We validate it without spending a
        download.
      </p>
      <ul>
        <li v-for="account in accounts" :key="account.id">
          {{ account.email }}
          <span :class="account.healthy ? 'healthy' : 'muted'">{{
            account.healthy ? "Connected" : "Needs attention"
          }}</span>
        </li>
      </ul>
      <form @submit.prevent="add">
        <label class="field"
          >Email<input
            v-model="email"
            type="email"
            required
            autocomplete="username" /></label
        ><label class="field"
          >Password<input
            v-model="password"
            type="password"
            required
            autocomplete="new-password" /></label
        ><button class="button" :disabled="busy">
          {{ busy ? "Validating…" : "Add household account" }}
        </button>
      </form>
    </section>
    <section class="panel">
      <h2>Agents</h2>
      <button class="button" @click="pair">Copy integration prompt</button
      ><label v-if="prompt" class="field"
        >Private, single-use setup prompt<textarea
          :value="prompt"
          readonly
          rows="7"
        />
      </label>
      <h3>Your connections</h3>
      <ul>
        <li v-for="grant in grants" :key="grant.id">
          <span
            >{{ grant.label || "Agent" }}
            <small v-if="grant.revoked"> · Revoked</small></span
          ><button
            v-if="!grant.revoked"
            class="button danger"
            @click="revoke(grant.id)"
          >
            Revoke
          </button>
        </li>
      </ul>
      <p v-if="!grants.length" class="muted">No agents paired yet.</p>
    </section>
  </div>
</template>
<style scoped>
.settings-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 24px;
  margin-top: 30px;
}
.panel p {
  font-size: 14px;
  line-height: 1.7;
}
.panel form {
  display: grid;
  gap: 16px;
  margin-top: 24px;
}
ul {
  list-style: none;
  padding: 0;
}
li {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 14px 0;
  border-bottom: 1px solid #334157;
  font-size: 13px;
  overflow-wrap: anywhere;
}
.healthy {
  color: var(--mint);
}
textarea {
  margin-top: 15px;
}
h3 {
  margin-top: 32px;
}
@media (max-width: 800px) {
  .settings-grid {
    grid-template-columns: 1fr;
  }
}
</style>
