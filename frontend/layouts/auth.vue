<template>
  <main class="auth-shell"><slot /></main>
</template>
<style scoped>
.auth-shell {
  min-height: 100dvh;
  display: grid;
  place-items: center;
  padding: 24px;
  background: radial-gradient(ellipse at top, #243149, var(--navy) 65%);
}
</style>
