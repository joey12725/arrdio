<script setup lang="ts">
    defineProps({
        card_title: String,
        add_title: String,
        addCard: Boolean,
        create_modal_open: Boolean 

    })

//     const zlibEmail = ref('')
//     const zlibPassword = ref('')
//     const showPassword = ref(false)

//    async function onClick(){

//     const zLibRequest = {"email": zlibEmail, "password": zlibPassword} 
//     const {data, error} = await useFetch<{id: String, download_count: String}>('/api/putZLibUser', {
//         method: 'put',
//         body: { request: zLibRequest }
//      });
//     }
</script>

<template>
    <div >
        <div v-if="!addCard">
            <UModal :title="card_title"  :ui="{      // dark neutral backdrop
            content: [
                '!bg-default',          // or  bg-neutral-800/100
                'border-2 border-primary',
                'shadow-[0_0_8px_0_rgba(54,209,178,0.6),0_0_20px_2px_rgba(54,209,178,0.9)]',
                'ring-offset-0',
                'divide-y divide-primary'
                ].join(' '),

                close: 'text-white hover:text-tertiary-600 '      // panel colour
  }">
  <!-- <template #close>
    <UButton
      variant="ghost"
      size="sm"
      icon="i-lucide-x"
      class="text-white hover:text-gray-200 left-5"
      aria-label="Close"
    />
  </template> -->
                <UCard  variant="solid"     :ui="{
        /* ----- ROOT STYLES ----- */
        root: [
          // border in your teal
          'bg-black-700',
          'border-2 border-primary',
          // neon glow: soft + hard
          'shadow-[0_0_8px_0_rgba(54,209,178,0.6),0_0_20px_2px_rgba(54,209,178,0.9)]',
          // make sure shadows stay outside the offset ring
          'ring-offset-0',
          'divide-y divide-primary',   
        ].join(' ')
      }">
                    <template #header>
                            <slot name="card-header"/>
                    </template>
                    <template #footer>
                        <slot name="card-footer"/>
                    </template>
                </UCard>
                <template #footer>
                    <slot name="edit-form-footer"/>
                </template>
            </UModal>
        </div>
        <div v-else-if="addCard">
            <UModal :title="add_title" :ui="{        // dark neutral backdrop
     content: ['!bg-default',
          'border-2 border-primary',
          // neon glow: soft + hard
          'shadow-[0_0_8px_0_rgba(54,209,178,0.6),0_0_20px_2px_rgba(54,209,178,0.9)]',
          // make sure shadows stay outside the offset ring
          'ring-offset-0',
          'divide-y divide-primary'].join(' '), 
           close: 'text-white hover:text-tertiary-600 '      // panel colour
  }" >
                <UCard :ui="{
        root: [
          'bg-black-700',                                 /* same dark fill */
          'border-2 border-primary',                    /* solid 2px teal edge */
          'shadow-[0_0_8px_0_rgba(54,209,178,0.6),0_0_20px_2px_rgba(54,209,178,0.9)]',
          'flex items-center justify-center'
        ].join(' ')
      }">
                    <UIcon name="icon-park-outline:add" class="size-10 text-primary place-self-center" />
                </UCard>
                <template #body>
                    <slot name="enterForm"/>

                </template>
            </UModal>
        </div>
    </div>
</template>