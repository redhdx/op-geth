package compiler

import (
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"runtime"
	"sync"
)

type CodeType uint8

var ErrFailPreprocessing = errors.New("fail to do preprocessing")
var ErrOptiDisabled = errors.New("Opcode optimization is disabled")

var initOnce sync.Once
var opcodeProcessor *OpcodeProcessor

const taskChannelSize = 1024 * 1024

type OpcodeProcessor struct {
	enabled     bool
	codeCache   *OpCodeCache
	taskChannel chan optimizeTask
}

type OpCodeProcessorConfig struct {
	DoOpcodeFusion bool
}

type optimizeTaskType byte

const (
	unknown  optimizeTaskType = 0
	generate optimizeTaskType = 1
	flush    optimizeTaskType = 2
)

type optimizeTask struct {
	taskType optimizeTaskType
	addr     common.Address
	codeHash common.Hash
	rawCode  []byte
}

func GetOpcodeProcessorInstance() *OpcodeProcessor {
	initOnce.Do(func() {
		opcodeProcessor = &OpcodeProcessor{
			enabled:     false,
			codeCache:   nil,
			taskChannel: make(chan optimizeTask, taskChannelSize),
		}
		// start task processors.
		taskNumber := max(runtime.NumCPU()*3/8, 1)

		for i := 0; i < taskNumber; i++ {
			go opcodeProcessor.taskProcessor()
		}
	})
	return opcodeProcessor
}

func (p *OpcodeProcessor) EnableOptimization() {
	if p.enabled {
		return
	}
	p.enabled = true
	p.codeCache = getOpCodeCacheInstance()
}

func (p *OpcodeProcessor) DisableOptimization() {
	p.enabled = false
}

// Producer functions
func (p *OpcodeProcessor) LoadOptimizedCode(address common.Address, hash common.Hash) OptCode {
	if !p.enabled {
		return nil
	}
	/* Try load from cache */
	codeCache := p.codeCache
	processedCode := codeCache.GetCachedCode(address, hash)
	return processedCode

}

func (p *OpcodeProcessor) GenOrLoadOptimizedCode(address common.Address, code []byte, hash common.Hash) {
	task := optimizeTask{generate, address, hash, code}
	p.taskChannel <- task
}

func (p *OpcodeProcessor) FlushCodeCache(address common.Address, hash common.Hash) {
	task := optimizeTask{flush, address, hash, nil}
	p.taskChannel <- task
}

func (p *OpcodeProcessor) RewriteOptimizedCodeForDB(address common.Address, code []byte, hash common.Hash) {
	if p.enabled {
		// p.GenOrRewriteOptimizedCode(address, code, hash)
		//
		p.GenOrLoadOptimizedCode(address, code, hash)
	}
}

// Consumer function
func (p *OpcodeProcessor) taskProcessor() {
	for {
		task := <-p.taskChannel
		// Process the message here
		p.handleOptimizationTask(task)
	}
}

func (p *OpcodeProcessor) handleOptimizationTask(task optimizeTask) {
	switch task.taskType {
	case generate:
		p.TryGenerateOptimizedCode(task.addr, task.rawCode, task.codeHash)
	case flush:
		p.DeleteCodeCache(task.addr, task.codeHash)
	}
}

// GenOrRewriteOptimizedCode generate the optimized code and refresh the codecache.
func (p *OpcodeProcessor) GenOrRewriteOptimizedCode(address common.Address, code []byte, hash common.Hash) (OptCode, error) {
	if !p.enabled {
		return nil, ErrOptiDisabled
	}
	processedCode, err := processByteCodes(code)
	if err != nil {
		log.Error("Can not generate optimized code: %s\n", err.Error())
		return nil, err
	}
	codeCache := p.codeCache
	err = codeCache.UpdateCodeCache(address, processedCode, hash)
	if err != nil {
		log.Error("Not update code cache", "err", err)
	}
	return processedCode, err
}

func (p *OpcodeProcessor) TryGenerateOptimizedCode(address common.Address, code []byte, hash common.Hash) (OptCode, bool, error) {
	if !p.enabled {
		return nil, false, ErrOptiDisabled
	}
	/* Try load from cache */
	codeCache := p.codeCache
	processedCode := codeCache.GetCachedCode(address, hash)
	hit := false
	var err error = nil
	if processedCode == nil || len(processedCode) == 0 {
		processedCode, err = p.GenOrRewriteOptimizedCode(address, code, hash)
		hit = false
	} else {
		hit = true
	}
	return processedCode, hit, err
}

func (p *OpcodeProcessor) DeleteCodeCache(addr common.Address, hash common.Hash) {
	if !p.enabled {
		return
	}
	// flush in case there are invalid cached code
	p.codeCache.RemoveCachedCode(addr, hash)
}

func processByteCodes(code []byte) (OptCode, error) {
	return doOpcodesProcess(code)
}

func doOpcodesProcess(code []byte) (OptCode, error) {
	code, err := doCodeFusion(code)
	if err != nil {
		return nil, ErrFailPreprocessing
	}
	return code, nil
}

func doCodeFusion(code []byte) ([]byte, error) {
	/*fusedCode := make([]byte, len(code))
	length := copy(fusedCode, code)
	skipToNext := false
	for i := 0; i < length; i++ {
		cur := i
		skipToNext = false

		if length > cur+4 {
			code0 := ByteCode(fusedCode[cur+0])
			code1 := ByteCode(fusedCode[cur+1])
			code2 := ByteCode(fusedCode[cur+2])
			code3 := ByteCode(fusedCode[cur+3])
			code4 := ByteCode(fusedCode[cur+4])
			if code0 == AND && code1 == SWAP1 && code2 == POP && code3 == SWAP2 && code4 == SWAP1 {
				op := AndSwap1PopSwap2Swap1
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				fusedCode[cur+2] = byte(Nop)
				fusedCode[cur+3] = byte(Nop)
				fusedCode[cur+4] = byte(Nop)
				skipToNext = true
			}

			// Test zero and Jump. target offset at code[2-3]
			if code0 == ISZERO && code1 == PUSH2 && code4 == JUMPI {
				op := JumpIfZero
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				fusedCode[cur+4] = byte(Nop)

				startMin := cur + 2
				endMin := cur + 4
				integer := new(uint256.Int)
				integer.SetBytes(common.RightPadBytes(
					fusedCode[startMin:endMin], 2))

				skipToNext = true
			}

			if skipToNext {
				i += 4
				continue
			}
		}

		if length > cur+3 {
			code0 := ByteCode(fusedCode[cur+0])
			code1 := ByteCode(fusedCode[cur+1])
			code2 := ByteCode(fusedCode[cur+2])
			code3 := ByteCode(fusedCode[cur+3])
			if code0 == SWAP2 && code1 == SWAP1 && code2 == POP && code3 == JUMP {
				op := Swap2Swap1PopJump
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				fusedCode[cur+2] = byte(Nop)
				fusedCode[cur+3] = byte(Nop)
				skipToNext = true
			}

			if code0 == SWAP1 && code1 == POP && code2 == SWAP2 && code3 == SWAP1 {
				op := Swap1PopSwap2Swap1
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				fusedCode[cur+2] = byte(Nop)
				fusedCode[cur+3] = byte(Nop)
				skipToNext = true
			}

			if code0 == POP && code1 == SWAP2 && code2 == SWAP1 && code3 == POP {
				op := PopSwap2Swap1Pop
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				fusedCode[cur+2] = byte(Nop)
				fusedCode[cur+3] = byte(Nop)
				skipToNext = true
			}
			// push and jump
			if code0 == PUSH2 && code3 == JUMP {
				op := Push2Jump
				fusedCode[cur] = byte(op)
				fusedCode[cur+3] = byte(Nop)
				skipToNext = true
			}

			if code0 == PUSH2 && code3 == JUMPI {
				op := Push2JumpI
				fusedCode[cur] = byte(op)
				fusedCode[cur+3] = byte(Nop)
				skipToNext = true
			}

			if code0 == PUSH1 && code2 == PUSH1 {
				op := Push1Push1
				fusedCode[cur] = byte(op)
				fusedCode[cur+2] = byte(Nop)
				skipToNext = true
			}

			if skipToNext {
				i += 3
				continue
			}
		}

		if length > cur+2 {
			code0 := ByteCode(fusedCode[cur+0])
			_ = ByteCode(fusedCode[cur+1])
			code2 := ByteCode(fusedCode[cur+2])
			if code0 == PUSH1 {
				if code2 == ADD {
					op := Push1Add
					fusedCode[cur] = byte(op)
					fusedCode[cur+2] = byte(Nop)
					skipToNext = true
				}
				if code2 == SHL {
					op := Push1Shl
					fusedCode[cur] = byte(op)
					fusedCode[cur+2] = byte(Nop)
					skipToNext = true
				}

				if code2 == DUP1 {
					op := Push1Dup1
					fusedCode[cur] = byte(op)
					fusedCode[cur+2] = byte(Nop)
					skipToNext = true
				}

			}
			if skipToNext {
				i += 2
				continue
			}
		}

		if length > cur+1 {
			code0 := ByteCode(fusedCode[cur+0])
			code1 := ByteCode(fusedCode[cur+1])

			if code0 == SWAP1 && code1 == POP {
				op := Swap1Pop
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				skipToNext = true
			}
			if code0 == POP && code1 == JUMP {
				op := PopJump
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				skipToNext = true
			}

			if code0 == POP && code1 == POP {
				op := Pop2
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				skipToNext = true
			}

			if code0 == SWAP2 && code1 == SWAP1 {
				op := Swap2Swap1
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				skipToNext = true
			}

			if code0 == SWAP2 && code1 == POP {
				op := Swap2Pop
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				skipToNext = true
			}

			if code0 == DUP2 && code1 == LT {
				op := Dup2LT
				fusedCode[cur] = byte(op)
				fusedCode[cur+1] = byte(Nop)
				skipToNext = true
			}

			if skipToNext {
				i++
				continue
			}
		}

		skip, steps := calculateSkipSteps(fusedCode, cur)
		if skip {
			i += steps
			continue
		}
	}
	return fusedCode, nil
	*/
	return code, nil
}

func calculateSkipSteps(code []byte, cur int) (skip bool, steps int) {
	inst := ByteCode(code[cur])
	if inst >= PUSH1 && inst <= PUSH32 {
		// skip the data.
		steps = int(inst - PUSH1 + 1)
		skip = true
		return skip, steps
	}

	switch inst {
	case Push2Jump, Push2JumpI:
		steps = 3
		skip = true
	case Push1Push1:
		steps = 3
		skip = true
	case Push1Add, Push1Shl, Push1Dup1:
		steps = 2
		skip = true
	case JumpIfZero:
		steps = 4
		skip = true
	default:
		return false, 0
	}
	return skip, steps
}
