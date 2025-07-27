<script setup lang="ts">


    defineProps({
        add_card_bool: Boolean,
        category: String,
        savepath: String
    })

    const toast = useToast()

    const emit = defineEmits<{
        (e: 'created'): void
        (e: 'deleted'): void
    }>()


    const newCategory = ref('')
    const newSavepath = ref('')


    const loadingOnAdd = ref(false)
    const errorOnAdd = ref(false)
    const successOnAdd = ref(false)

    const addModalOpen = ref(false)

    async function onClick(){
        console.log("test")
        loadingOnAdd.value = true
        successOnAdd.value = false
        errorOnAdd.value = false
        
        const categoryRequest = {"category": newCategory, "savepath": newSavepath} 
        const {data, error} = await useFetch<{category: String, savepath: String}>('/api/putDownloadCategory', {
            method: 'put',
            body: categoryRequest
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
        
        // addModalOpen.value = false
    
        emit('created')
    }

    async function onDelete(category:string | undefined){
        // console.log("Test")
        const {data, error} = await useFetch('/api/deleteDownloadCategory', {
            method: 'delete',
            body: category
        });

        if (!error.value){
            emit("deleted")
        }
    }

    
</script>

<template>
    <Card card_title="Edit Download Category" add_title="Add Download Category" :addCard="add_card_bool">
        <template #card-header>
            <h3 class="font-header text-primary text-lg leading-none tracking-wide">
                {{ category }}
            </h3>
        </template>
        <template #card-footer>
            <div class="inline-flex items-center rounded-md bg-green-700 px-2 py-1 text-xs font-medium text-white ring-1 ring-gray-500/10 ring-inset">
                {{ savepath  }}
            </div>            
        </template>
        <template #edit-form-body>

        </template>
        <template #edit-form-footer>
            <UButton color="error" @click="onDelete(category)" loading-auto :disabled="category === 'audiobooks' || category === 'ebooks'">Delete</UButton>
        </template>
        <template #enterForm>
            <div class="flex gap-2">
                <div class="flex flex-row gap-2">
                    <UInput v-model="newCategory" type="text" placeholder="Category" />
                    <UInput v-model="newSavepath" placeholder="Savepath" type="text"/>
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