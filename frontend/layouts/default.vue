<script setup lang="js">
import { useMediaQuery} from '@vueuse/core'

const isMobile = useMediaQuery('(max-width: 1024px)')


</script>

<template>
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <div class="flex flex-col h-screen w-screen">
    <HeaderBar />
    
      <MobileNav v-if="isMobile"/>
      
    
    <div class="flex flex-1 overflow-hidden">
      <Sidebar v-if="!isMobile"/>
      <main class="flex-1 p-4 bg-neutral flex">
        <UApp>
          <NuxtPage />
        </UApp>
      </main>
    </div>
  </div>
</template>