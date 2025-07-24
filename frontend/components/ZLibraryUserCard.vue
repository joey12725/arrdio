<script setup lang="ts">
import { boolean } from 'valibot'

    defineProps({
        add_card_bool: Boolean,
        email: String,
        download_count: Number
    })

    const toast = useToast()

    const emit = defineEmits<{
        (e: 'created'): void
        (e: 'deleted'): void
    }>()


    const zlibEmail = ref('')
    const zlibPassword = ref('')
    const showPassword = ref(false)

    const loadingOnAdd = ref(false)
    const errorOnAdd = ref(false)
    const successOnAdd = ref(false)

    const addModalOpen = ref(false)

    async function onClick(){
        loadingOnAdd.value = true
        successOnAdd.value = false
        errorOnAdd.value = false
        
        const zLibRequest = {"email": zlibEmail, "password": zlibPassword} 
        const {data, error} = await useFetch<{id: String, download_count: String}>('/api/putZLibUser', {
            method: 'put',
            body: zLibRequest
        });

        if (error.value) {
            loadingOnAdd.value = false
            errorOnAdd.value = true
            toast.add({title: "Uh oh! Panikk ahhhh, there was an error!", 
            description: String(error.value?.data.statusMessage || error.value),
            icon: 'i-heroicons-exclamation-triangle',
            color: 'error'
            })
            return
        }

        loadingOnAdd.value = false
        successOnAdd.value = true
        toast.add({title: "Success!", 
            description: "Download path saved successfully",
            icon: 'lucide:check',
            color: 'success'
        })
        
        addModalOpen.value = false
    
        emit('created')
    }

    async function onDelete(email:string | undefined){
        // console.log("Test")
        const {data, error} = await useFetch('/api/deleteZLibUser', {
            method: 'delete',
            body: email
        });

        if (!error.value){
            emit("deleted")
        }
    }

</script>

<template>
    <Card card_title="Edit ZLibrary User" add_title="Add ZLibrary User" :addCard="add_card_bool">
        <template #card-header>
            <h3 class="font-header text-primary text-lg leading-none tracking-wide">
                {{ email }}
            </h3>
        </template>
        <template #card-footer>
            <div class="inline-flex items-center rounded-md bg-green-700 px-2 py-1 text-xs font-medium text-white ring-1 ring-gray-500/10 ring-inset">
                {{ download_count  }} / 10
            </div>            
        </template>
        <template #edit-form-body>

        </template>
        <template #edit-form-footer>
            <UButton color="error" @click="onDelete(email)" loading-auto >Delete</UButton>
        </template>
        <template #enterForm>
            <div class="flex gap-2">
                <div class="flex flex-row gap-2">
                    <UInput v-model="zlibEmail" type="text" placeholder="Email" />
                    <UInput v-model="zlibPassword" placeholder="Password" :type="showPassword ? 'text' : 'password'">
                        <template #trailing>
                            <UButton
                            color="primary"
                            variant="link"
                            size="sm"
                            :icon="showPassword ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                            :aria-label="showPassword ? 'Hide password' : 'Show password'"
                            :aria-pressed="zlibPassword"
                            aria-controls="zlibPassword"
                            @click="showPassword = !showPassword"
                            />
                        </template>
                    </UInput>
                </div>
                <UButton 
                    size="sm" 
                    @click="onClick" 
                    :loading="loadingOnAdd"
                    :trailing-icon=" 
                    successOnAdd ? 'i-lucide-check-circle' :
                    errorOnAdd   ? 'i-lucide-alert-circle' :
                    'i-lucide-arrow-right'">Submit
                </UButton>
            </div>
        </template>
    </Card>
</template>