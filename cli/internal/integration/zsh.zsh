# termind shell integration (M2)
#
# 这个脚本由 ~/.zshrc 无条件 source,但只在 termind 包装的 shell 里激活
# (通过 TERMIND_SHELL 环境变量判断)。普通 zsh 零开销。
#
# 在每条命令开始/结束时吐 OSC 133 暗号,让 termind 识别命令边界:
#   A          prompt 开始
#   C          命令开始执行
#   D;<exit>   命令结束(带退出码)
#
# B(prompt 结束/输入开始)M2 不注入 PS1 —— 后续 M5 prompt 抑制需要时再加。
# termind 诊断输出结束时会给子 zsh 发 SIGUSR1;这里用 zle 自己刷新显示,
# 不往 PTY 注入 Ctrl-R 之类会改变用户输入状态的按键。

# 不在 termind shell 里就立刻 return,普通 zsh 零代价
[[ -z "$TERMIND_SHELL" ]] && return 0

# 防止重复 source(同一 shell 内多次 source 不会出错)
[[ -n "$_TERMIND_INTEGRATION_LOADED" ]] && return 0
typeset -g _TERMIND_INTEGRATION_LOADED=1

# precmd:在显示 prompt 前触发
# - 第一次只发 A(还没有"上条命令"的 D)
# - 之后每次先发上条命令的 D;<exit>,再发本次新 prompt 的 A
__termind_precmd() {
    local exit=$?
    if [[ -n "$_TERMIND_RAN_ONCE" ]]; then
        printf '\e]133;D;%s\a' "$exit"
    fi
    typeset -g _TERMIND_RAN_ONCE=1
    printf '\e]133;A\a'
}

# preexec:命令执行前触发(用户已按回车)
__termind_preexec() {
    printf '\e]133;C\a'
}

TRAPUSR1() {
    [[ -o zle ]] && zle -I
}

autoload -Uz add-zsh-hook
add-zsh-hook precmd __termind_precmd
add-zsh-hook preexec __termind_preexec
