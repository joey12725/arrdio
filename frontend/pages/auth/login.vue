<!-- pages/auth/login.vue -->
<script setup lang="ts">
definePageMeta({ layout: 'auth' })

import { useOidcAuth } from '#imports'

const { login } = useOidcAuth()



</script>

<template>
  <div class="min-h-screen flex flex-col items-center justify-center">
    <h1 class="text-3xl font-semibold mb-6">Sign in</h1>
    <button
      type="button"                  
      class="px-6 py-2 bg-blue-600 text-white rounded hover:bg-blue-700"
      @click="login('oidc')"               
    >
      Login with Authentik
    </button>
  </div>
</template>
