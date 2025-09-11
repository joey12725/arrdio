<script setup lang="ts">
    import { useMediaQuery} from '@vueuse/core'

    const isMobile = useMediaQuery('(max-width: 1024px)')
    
    interface DownloadCategory {
        id: number
        category: string
        savepath: string
    }
    const categoryFetch = useFetch<DownloadCategory[]>('/api/getCategory')

    const { data, refresh } = toRefs(categoryFetch)

    function onRefresh(){
        refresh.value!()
    }
    
</script>

<template>
    <section class="h-full w-full overflow-auto">
        <div class="flex flex-col items-left">
        
            <h1 class="font-header text-primary  text-[40px] text-shadow[#36d1b2] leading-none tracking-wide ">Download Categories</h1>
            
            <hr class="flex border-0 border-t-2 border-green-600 w-sm lg:w-lg mt-2 mb-8" />
        </div>
    
        <div class="flex flex-col lg:flex-row gap-8">
            <div class="flex flex-col lg:flex-row gap-8">
                <DownloadCategoryCard v-for="downloadcategory in data" 
                :key="downloadcategory.id"
                :category="downloadcategory.category"
                :savepath="downloadcategory.savepath"
                :add_card_bool="false"
                @deleted="onRefresh"/>
            </div>
            <div>
                <DownloadCategoryCard 
                :add_card_bool="true"
                @created="onRefresh"/>
            </div>
        </div>
    </section>
</template>