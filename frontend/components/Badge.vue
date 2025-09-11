<script setup lang="ts">
import { badge } from '#build/ui';

    interface Props {
        badgeStatus: 'pending' | 'loading' | 'found' | 'error' | 'downloaded' | 'notfound'
    }
    
    const props = defineProps<Props>()





    function setColor(){
        const color = {
           found: 'success' as const,
            error: 'error' as const,
            pending: 'warning' as const,
            loading: 'info' as const,
            downloaded: 'success' as const,
            notfound: 'error' as const
        }[props.badgeStatus]

        // if (props.badgeStatus === "pending"){

        // }
        return color
    }
</script>

<template>

<UBadge class='capitalize' variant = 'subtle' :color = "setColor()" :label="props.badgeStatus" />
</template>