#!/usr/bin/env python3
"""Exercise the built editor in an actual Unix PTY (no third-party packages)."""
import errno
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import sys
import tempfile
import termios
import time
import fcntl

binary = str(Path(sys.argv[1] if len(sys.argv) > 1 else './atto').resolve())
with tempfile.TemporaryDirectory(prefix='atto-pty-') as directory:
    first = Path(directory) / 'first.txt'
    second = Path(directory) / 'second.txt'
    pid, fd = pty.fork()
    if pid == 0:
        os.environ['TERM'] = 'xterm-256color'
        os.execv(binary, [binary, str(first), str(second)])
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
    output = bytearray()
    exited = False

    def drain(timeout=0.1):
        if select.select([fd], [], [], timeout)[0]:
            try:
                data = os.read(fd, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    return
                raise
            output.extend(data)

    def shown(needle):
        # tcell can insert cursor/style escapes between words in a redraw.
        plain = re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0-2A-Z0-9]|\x1b[=>]', b'', bytes(output))
        return b''.join(needle.split()) in b''.join(plain.split())

    def wait_for(predicate, description):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            drain()
            if predicate():
                return
        raise AssertionError(f'timed out: {description}; output tail: {bytes(output[-800:])!r}')

    try:
        wait_for(lambda: shown(b'Ctrl-G'), 'initial screen')
        os.write(fd, '日本語\nfirst'.replace('\n', '\r').encode() + b'\x13')
        wait_for(lambda: first.exists() and first.read_text() == '日本語\nfirst', 'first save')
        output.clear()
        os.write(fd, b'\x1b[17~')  # xterm F6
        wait_for(lambda: shown(b'second.txt'), 'switch to second buffer')
        os.write(fd, b'second\rline\x13')
        wait_for(lambda: second.exists() and second.read_text() == 'second\nline', 'second save')
        output.clear()
        os.write(fd, b'\x1b[15~')  # xterm F5
        wait_for(lambda: shown(b'first.txt'), 'return to first buffer')
        output.clear()
        os.write(fd, b'\x1bOR')  # xterm F3: left/right split
        wait_for(lambda: shown(b'Pane 2/2'), 'vertical split')
        output.clear()
        os.write(fd, b'\x1b[18~')  # F7: focus other pane
        wait_for(lambda: shown(b'Pane 1/2'), 'focus other pane')
        output.clear()
        os.write(fd, b'\x1bOS')  # F4: top/bottom split
        wait_for(lambda: '─'.encode() in output, 'horizontal split')
        output.clear()
        os.write(fd, b'\x1b[19~')  # F8 closes pane, keeps second buffer
        wait_for(lambda: shown(b'Pane closed'), 'close pane without closing buffer')
        output.clear()
        os.write(fd, b'\x01\x1b[A\x01\x1b[C\x1br')  # col 2, Alt-R
        wait_for(lambda: shown(b'RECT'), 'rectangle mode')
        os.write(fd, b'\x1b[B#')  # insert into the same column of both lines
        output.clear()
        os.write(fd, b'\x1b')
        wait_for(lambda: shown(b'Selection cleared'), 'finish rectangle')
        os.write(fd, b'\x13')
        wait_for(lambda: second.read_text() == 's#econd\nl#ine', 'rectangle save')
        output.clear()
        os.write(fd, b'\x10')  # Ctrl-P
        wait_for(lambda: shown(b'Project directory:'), 'project directory prompt')
        output.clear()
        os.write(fd, b'\x15' + directory.encode() + b'\r')
        wait_for(lambda: shown(b'Project search:'), 'project query prompt')
        output.clear()
        os.write(fd, b'#\r')
        wait_for(lambda: shown(b'2 matches'), 'project search completion')
        output.clear()
        os.write(fd, b'\r')
        wait_for(lambda: shown(b'Project match:'), 'project result navigation')
        os.write(fd, b'\x11')  # Ctrl-Q: all buffers are now clean
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            drain()
            result, status = os.waitpid(pid, os.WNOHANG)
            if result:
                exited = True
                assert os.waitstatus_to_exitcode(status) == 0, status
                break
        assert exited, 'editor did not quit'
        assert b'\x1b[?1049l' in output, 'alternate screen was not restored'
        print('PTY smoke passed: Japanese, saves, split/focus/unsplit, rectangle, project search/jump, quit and terminal restoration')
    finally:
        if not exited:
            os.kill(pid, signal.SIGTERM)
            os.waitpid(pid, 0)
        os.close(fd)
