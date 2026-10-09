export const routinesCopy = {
 title: 'Rotinas do Bot',
 serverOnly: 'Rotinas só rodam com o servidor do DAIMON aberto. Cada disparo usa as mesmas políticas, limites e aprovações. Não há recuperação de horários perdidos.',
 limits: 'Até 4 rotinas ativas por Bot e 32 no total. Intervalo mínimo entre disparos do mesmo Bot: 15 minutos. Computadores pagos na nuvem não são disparados por rotinas.',
 states: {active:'Ativa',paused:'Pausada',running:'Em execução',waiting_approval:'Aguardando aprovação',failed:'Falha no disparo'} as Record<string,string>,
};
