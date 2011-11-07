include $(GOROOT)/src/Make.inc

GOINSTALLED = ${GOPATH}/pkg/${GOOS}_${GOARCH}
SRCS = $(shell grep '^package.*main' *.go | cut -d: -f1)
PRGS = $(SRCS:.go=) prolix
LIB_SRCS = $(filter-out $(SRCS), $(wildcard *.go))

all:$(PRGS)

lib.$(O):$(LIB_SRCS)    
	$(GC) -I$(GOINSTALLED) -o $@ $^ 

clean:
	rm -f $(PRGS) *.$(O)

$(PRGS): lib.$(O)

%:%.go
	$(GC) -I. -I$(GOINSTALLED) $< && $(LD) -L. -L$(GOINSTALLED) -o $@ $@.$(O)
