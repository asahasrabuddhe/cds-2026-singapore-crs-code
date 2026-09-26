# 05. Init: what PID 1 owes everybody else

## What it shows

PID 1 of a PID namespace has two jobs the command you asked to run almost
certainly does not do.

**Reaping.** When a process exits, its parent has to `wait` on it, or it
stays in the process table as a zombie. When a parent exits before its
children, the kernel re-parents those children to PID 1, and from then on
only PID 1 can reap them. A container whose PID 1 is `sleep` or `nginx`
collects zombies for as long as it runs.

**Signals.** The kernel refuses to deliver a signal with the default
disposition to the init of a PID namespace, on the grounds that killing
init by accident would be worse. So `kill -TERM` at a container whose PID
1 never installed a handler does nothing. This is why `docker stop`
waits ten seconds and sends SIGKILL, and why Docker has `--init` and tini
exists.

`internal/initproc` is the runtime's answer: the child stays resident as
PID 1, starts the command as its child, forwards every catchable signal to
it, reaps everything that comes back with `wait4(-1)`, and exits with the
command's code, 128 plus the signal number if it died of one. The runtime
process outside forwards its own signals in too, so Ctrl-C in the
terminal reaches the command the way it does under `docker run`. `-init=false`
turns the init off and execs the command as PID 1, which is how you get
the two failures to look at.

## How to run it

```console
$ ./scripts/demo.sh 5
```

The beat starts each container in the background and looks at it from the
host with `ps`, which sees the container's processes with their real
state. By hand, in two terminals:

```console
$ sudo ./mycontainer run -init=false rootfs/busybox /bin/sh -c 'sh -c "sleep 0.3 &"; exec sleep 30'
# ...in the other:
$ ps -eo pid,ppid,stat,comm | grep -E 'sleep|Z'
$ sudo kill -TERM <pid of sleep>          # nothing happens
```

## Expected output

Without init, `ps` shows `sleep` as the container's PID 1 and under it a
`sh` in state `Z`, the background job's shell, orphaned onto PID 1 and
never collected. (busybox runs `sleep` inside the forked shell rather than
exec'ing it, hence the name.) SIGTERM to that `sleep` does nothing; it is
still there a second later.

With init, `ps` shows `exe` as PID 1, `sleep` under it, and no zombie.
SIGTERM to PID 1 is forwarded to `sleep`, which dies of it, and the
container exits 143.

## What it proves

A container is not a process; it is a process tree, and PID 1 is
responsible for the tree. Every serious runtime ships an init or tells you
to bring one. Ours is fifty lines and it is in the repository because the
two failures above are the ones people meet in production and do not
recognise.
