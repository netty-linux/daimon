export const startupReasons = {
 environment_failed: 'Não foi possível preparar o ambiente do driver.',
 handshake_failed: 'Falha na inicialização do protocolo do driver.',
 discovery_failed: 'Falha ao descobrir as ferramentas do driver.',
 discovery_too_large: 'A descrição ou o frame de descoberta excedeu o limite permitido.',
 tool_limit_exceeded: 'A quantidade de ferramentas excedeu o limite permitido.',
 timeout: 'O tempo de inicialização foi esgotado.',
 cancelled: 'A inicialização foi cancelada.',
 protocol_error: 'O driver enviou uma resposta incompatível com o protocolo.',
 unknown: 'Não foi possível classificar a causa da falha.',
} as const;
export type StartupReason = keyof typeof startupReasons;
