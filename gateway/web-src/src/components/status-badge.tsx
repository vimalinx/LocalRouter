import { CircleAlert, CircleCheck, CircleOff } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import type { ProtocolView } from '@/lib/types'

export function ProtocolStatusBadge(props: { protocol: ProtocolView }) {
  if (props.protocol.ready) {
    return (
      <Badge variant='success'>
        <CircleCheck aria-hidden='true' className='size-3.5' />
        {props.protocol.status_label}
      </Badge>
    )
  }

  if (!props.protocol.enabled) {
    return (
      <Badge variant='secondary'>
        <CircleOff aria-hidden='true' className='size-3.5' />
        {props.protocol.status_label}
      </Badge>
    )
  }

  return (
    <Badge variant='warning'>
      <CircleAlert aria-hidden='true' className='size-3.5' />
      {props.protocol.status_label}
    </Badge>
  )
}
